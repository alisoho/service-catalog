// Copyright 2016 Fraunhofer Institute for Applied Information Technology FIT

package catalog

import (
	"encoding/json"
	"log"
	"strings"
	"sync"
	"time"

	paho "github.com/eclipse/paho.mqtt.golang"
	mqtttopic "github.com/farshidtz/mqtt-match"
	uuid "github.com/satori/go.uuid"
)

const (
	mqttServiceTTL               = 1 * time.Minute
	mqttServiceHeartbeatInterval = mqttServiceTTL / 2
	mqttServiceName              = "_mqtt._tcp"
)

type MQTTManager struct {
	sync.Mutex
	controller  *Controller
	scID        string
	topicPrefix string

	clients []MQTTClient
}

type MQTTClient struct {
	MQTTClientConf
	paho       paho.Client
	topics     []string
	willTopics []string
	manager    *MQTTManager
}

func StartMQTTManager(controller *Controller, mqttConf MQTTConf, scID string) {
	c := &MQTTManager{
		controller:  controller,
		scID:        scID,
		topicPrefix: mqttConf.TopicPrefix,
	}
	controller.AddListener(c)

	for _, clientConf := range append(mqttConf.AdditionalClients, mqttConf.Client) {
		if clientConf.BrokerURI == "" {
			continue
		}

		var client MQTTClient
		client.MQTTClientConf = clientConf
		client.manager = c

		if client.BrokerID == "" {
			client.BrokerID = uuid.NewV4().String()
		}

		client.willTopics = append(mqttConf.CommonWillTopics, client.WillTopics...)

		for _, topics := range [][]string{mqttConf.CommonRegTopics, mqttConf.CommonWillTopics, client.RegTopics, client.WillTopics} {
			client.topics = append(client.topics, topics...)
		}

		go client.connect()
	}
}

func (m *MQTTManager) registerAsService(client *MQTTClient) {
	service := Service{
		ID:          client.BrokerID,
		Name:        mqttServiceName,
		Description: "MQTT Broker",
		Meta: map[string]interface{}{
			"registrator": m.scID,
		},
		APIs: map[string]string{
			APITypeMQTT: client.BrokerURI,
		},
		TTL: uint(mqttServiceTTL / time.Second),
	}

	for ; true; <-time.NewTicker(mqttServiceHeartbeatInterval).C {
		m.addService(service)
	}
}

func (c *MQTTClient) connect() {
	for {
		opts, err := c.pahoOptions()
		if err != nil {
			log.Fatalf("Unable to configure MQTT c: %s", err)
		}
		// Add handlers
		opts.SetOnConnectHandler(c.onConnect)
		opts.SetConnectionLostHandler(c.onDisconnect)
		opts.SetAutoReconnect(true)

		c.paho = paho.NewClient(opts)
		logger.Printf("MQTT: %s: Connecting...", c.BrokerURI)

		if token := c.paho.Connect(); token.Wait() && token.Error() != nil {
			log.Printf("Error connecting to broker: %v", token.Error())
			continue
			// TODO sleep?
		}

		go c.manager.registerAsService(c)
		break
	}
}

//Controller Listener interface implementation
func (m *MQTTManager) added(s Service) {
	if len(m.clients) > 0 {
		m.publishAliveService(s)
	}
}

//Controller Listener interface implementation
func (m *MQTTManager) updated(s Service) {
	if len(m.clients) > 0 {
		m.publishAliveService(s)
	}
}

//Controller Listener interface implementation
func (m *MQTTManager) deleted(s Service) {
	if len(m.clients) > 0 {
		m.publishDeadService(s)
	}
}

func (m *MQTTManager) publishAliveService(s Service) {
	payload, err := json.Marshal(s)
	if err != nil {
		logger.Printf("MQTT: Error parsing json: %s ", err)
		return
	}
	topic := m.topicPrefix + s.Name + "/" + s.ID + "/alive"
	logger.Printf("MQTT: Publishing service %s to topic %s", s.ID, topic)
	for _, client := range m.clients {
		if token := client.paho.Publish(topic, 1, true, payload); token.Wait() && token.Error() != nil {
			logger.Printf("MQTT: %s: Error publishing: %v", client.BrokerURI, token.Error())
		}
	}
}

func (m *MQTTManager) publishDeadService(s Service) {
	// remove the retained message
	topic := m.topicPrefix + s.Name + "/" + s.ID + "/alive"
	logger.Printf("MQTT: Removing the retain message topic: %s", topic)
	for _, client := range m.clients {
		if token := client.paho.Publish(topic, 1, true, ""); token.Wait() && token.Error() != nil {
			logger.Printf("MQTT: %s: Error publishing: %v", client.BrokerURI, token.Error())
		}
	}

	// publish dead message
	topic = m.topicPrefix + s.Name + "/" + s.ID + "/dead"
	logger.Printf("MQTT: Publishing deletion Service topic:%s", topic)
	payload, err := json.Marshal(s)
	if err != nil {
		logger.Printf("MQTT: Error parsing json: %s ", err)
		return
	}
	for _, client := range m.clients {
		if token := client.paho.Publish(topic, 1, false, payload); token.Wait() && token.Error() != nil {
			logger.Printf("MQTT: %s: Error publishing: %v", client.BrokerURI, token.Error())
		}
	}
}

func (c *MQTTClient) onConnect(pahoClient paho.Client) {
	logger.Printf("MQTT: %s: Connected.", c.BrokerURI)

	for _, topic := range c.topics {
		if token := pahoClient.Subscribe(topic, c.QoS, c.onMessage); token.Wait() && token.Error() != nil {
			logger.Printf("MQTT: %s: Error subscribing: %v", c.BrokerURI, token.Error())
		}
		logger.Printf("MQTT: %s: Subscribed to %s", c.BrokerURI, topic)
	}
}

func (c *MQTTClient) onDisconnect(pahoClient paho.Client, err error) {
	logger.Printf("MQTT: %s: Disconnected: %s", c.BrokerURI, err)
}

func (c *MQTTClient) onMessage(_ paho.Client, msg paho.Message) {
	topic, payload := msg.Topic(), msg.Payload()
	logger.Debugf("MQTT: %s %s", topic, payload)

	// Will message has ID in topic
	// Get id from topic. Expects: <prefix>will/<id>
	for _, filter := range c.willTopics {
		if mqtttopic.Match(filter, topic) {
			if parts := strings.SplitAfter(msg.Topic(), "will/"); len(parts) == 2 && parts[1] != "" {
				c.manager.removeService(Service{ID: parts[1]})
				return
			}
		}
	}

	// Get id from topic. Expects: <prefix>service/<id>
	var id string
	if parts := strings.SplitAfter(msg.Topic(), "service/"); len(parts) == 2 {
		id = parts[1]
	}

	var service Service
	err := json.Unmarshal(payload, &service)
	if err != nil {
		logger.Printf("MQTT: Error parsing json: %s : %v", payload, err)
		return
	}

	if service.ID == "" && id == "" {
		logger.Printf("MQTT: Invalid registration: ID not provided")
		return
	} else if service.ID == "" {
		logger.Debugf("MQTT: Getting id from topic: %s", id)
		service.ID = id
	}

	c.manager.addService(service)
}

func (m *MQTTManager) addService(service Service) {
	_, err := m.controller.update(service.ID, service)
	if err != nil {
		switch err.(type) {
		case *NotFoundError:
			// Create a new service with the given id
			_, err := m.controller.add(service)
			if err != nil {
				switch err.(type) {
				case *BadRequestError:
					logger.Printf("MQTT: Invalid service: %s", err)
				default:
					logger.Printf("MQTT: Error adding service: %s", err)
				}
			} else {
				logger.Printf("MQTT: Added service: %s", service.ID)
			}
		case *BadRequestError:
			logger.Printf("MQTT: Invalid service: %s", err)
		default:
			logger.Printf("MQTT: Error updating service: %s", err)
		}
	} else {
		logger.Printf("MQTT: Updated service: %s", service.ID)
	}
}

func (m *MQTTManager) removeService(service Service) {
	err := m.controller.delete(service.ID)
	if err != nil {
		logger.Printf("MQTT: Error removing service: %s: %s", service.ID, err)
		return
	}
	logger.Printf("MQTT: Removed service: %s", service.ID)
}
