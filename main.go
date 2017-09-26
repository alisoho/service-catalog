// Copyright 2014-2016 Fraunhofer Institute for Applied Information Technology FIT

package main

import (
	"flag"
	"fmt"
	"mime"
	"net/http"
	_ "net/http/pprof"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	_ "code.linksmart.eu/com/go-sec/auth/keycloak/validator"
	"code.linksmart.eu/com/go-sec/auth/validator"
	"code.linksmart.eu/sc/service-catalog/catalog"
	"github.com/codegangsta/negroni"
	"github.com/gorilla/context"
	"github.com/justinas/alice"
	"github.com/oleksandr/bonjour"
)

var (
	confPath = flag.String("conf", "conf/service-catalog.json", "Service catalog configuration file path")
	profile  = flag.Bool("profile", false, "Enable the HTTP server for runtime profiling")
)

func main() {
	flag.Parse()
	if *profile {
		logger.Println("Starting runtime profiling server")
		go func() { logger.Println(http.ListenAndServe("0.0.0.0:6060", nil)) }()
	}

	config, err := loadConfig(*confPath)
	if err != nil {
		logger.Fatalf("Error reading config file %v: %v", *confPath, err)
	}

	r, shutdownAPI, err := setupRouter(config)
	if err != nil {
		logger.Fatal(err.Error())
	}

	// Announce service using DNS-SD
	var bonjourS *bonjour.Server
	if config.DnssdEnabled {
		bonjourS, err = bonjour.Register(config.Description,
			catalog.DNSSDServiceType,
			"",
			config.BindPort,
			[]string{fmt.Sprintf("uri=%s", config.ApiLocation)},
			nil)
		if err != nil {
			logger.Printf("Failed to register DNS-SD service: %s", err.Error())
		} else {
			logger.Println("Registered service via DNS-SD using type", catalog.DNSSDServiceType)
		}
	}

	// Setup signal catcher for the server's proper shutdown
	c := make(chan os.Signal, 1)
	signal.Notify(c,
		syscall.SIGHUP,
		syscall.SIGINT,
		syscall.SIGTERM,
		syscall.SIGQUIT)
	go func() {
		for _ = range c {
			// sig is a ^C, handle it

			//TODO: put here the last will logic

			// Stop bonjour registration
			if bonjourS != nil {
				bonjourS.Shutdown()
				time.Sleep(1e9)
			}

			// Shutdown catalog API
			err := shutdownAPI()
			if err != nil {
				logger.Println(err.Error())
			}

			logger.Println("Stopped")
			os.Exit(0)
		}
	}()

	err = mime.AddExtensionType(".jsonld", "application/ld+json")
	if err != nil {
		logger.Println("ERROR: ", err.Error())
	}

	// Configure the middleware
	n := negroni.New(
		negroni.NewRecovery(),
		negroni.NewLogger(),
		&negroni.Static{
			Dir:       http.Dir(config.StaticDir),
			Prefix:    catalog.StaticLocation,
			IndexFile: "index.html",
		},
	)
	// Mount router
	n.UseHandler(r)

	// Start listener
	endpoint := fmt.Sprintf("%s:%s", config.BindAddr, strconv.Itoa(config.BindPort))
	logger.Printf("Starting standalone Service Catalog at %v%v", endpoint, config.ApiLocation)
	n.Run(endpoint)
}

func setupRouter(config *Config) (*router, func() error, error) {
	var listeners []catalog.Listener

	// Setup API storage
	var (
		storage catalog.CatalogStorage
		err     error
	)
	switch config.Storage.Type {
	case catalog.CatalogBackendMemory:
		storage = catalog.NewMemoryStorage()
	case catalog.CatalogBackendLevelDB:
		storage, err = catalog.NewLevelDBStorage(config.Storage.DSN, nil)
		if err != nil {
			return nil, nil, fmt.Errorf("Failed to start LevelDB storage: %v", err.Error())
		}
	default:
		return nil, nil, fmt.Errorf("Could not create catalog API storage. Unsupported type: %v", config.Storage.Type)
	}

	controller, err := catalog.NewController(storage, config.ApiLocation, listeners...)
	if err != nil {
		storage.Close()
		return nil, nil, fmt.Errorf("Failed to start the controller: %v", err.Error())
	}

	// Create catalog API object
	api := catalog.NewCatalogAPI(
		controller,
		config.ApiLocation,
		catalog.StaticLocation,
		config.Description,
	)

	commonHandlers := alice.New(
		context.ClearHandler,
	)

	// Append auth handler if enabled
	if config.Auth.Enabled {
		// Setup ticket validator
		v, err := validator.Setup(
			config.Auth.Provider,
			config.Auth.ProviderURL,
			config.Auth.ServiceID,
			config.Auth.BasicEnabled,
			config.Auth.Authz)
		if err != nil {
			return nil, nil, err
		}

		commonHandlers = commonHandlers.Append(v.Handler)
	}

	// Configure http api router
	r := newRouter()
	// Handlers
	r.get(config.ApiLocation, commonHandlers.ThenFunc(api.List))
	r.post(config.ApiLocation, commonHandlers.ThenFunc(api.Post))
	// Accept an id with zero or one slash: [^/]+/?[^/]*
	// -> [^/]+ one or more of anything but slashes /? optional slash [^/]* zero or more of anything but slashes
	r.get(config.ApiLocation+"/{id:[^/]+/?[^/]*}", commonHandlers.ThenFunc(api.Get))
	r.put(config.ApiLocation+"/{id:[^/]+/?[^/]*}", commonHandlers.ThenFunc(api.Put))
	r.delete(config.ApiLocation+"/{id:[^/]+/?[^/]*}", commonHandlers.ThenFunc(api.Delete))
	r.get(config.ApiLocation+"/{path}/{op}/{value:.*}", commonHandlers.ThenFunc(api.Filter))

	return r, controller.Stop, nil
}
