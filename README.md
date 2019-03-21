# Service Catalog
[![GoDoc](https://godoc.org/code.linksmart.eu/sc/service-catalog?status.svg)](https://godoc.org/code.linksmart.eu/sc/service-catalog)
[![Build Status](https://pipelines.linksmart.eu/plugins/servlet/wittified/build-status/SC-BUILD)](https://pipelines.linksmart.eu/browse/SC-BUILD)

LinkSmart Service Catalog is a registry enabling discovery of other web services via a RESTful API or through an MQTT broker.
 
* [Documentation](https://docs.linksmart.eu/display/SC)
* [Issue Tracking](https://boards.linksmart.eu/issues/?jql=project+%3D+LS+AND+component+%3D+%22Service+Catalog%22)

## Run
The following command runs the latest release of service catalog with the default configurations:
```
docker run -p 8082:8082 docker.linksmart.eu/sc
```
Other images and binary distributions can be found from the documentation.

## Development
The dependencies of this package are managed by [Go Modules](https://blog.golang.org/using-go-modules).

To compile from source:
```
git clone https://code.linksmart.eu/scm/sc/service-catalog.git
cd service-catalog
go build
```