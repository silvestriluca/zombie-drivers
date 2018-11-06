/*
Gateway service for Zombie test.

*/

package main

//Import statements
import (
	"bytes"
	"encoding/json"
	"io/ioutil"
	"log"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"gopkg.in/yaml.v2"
)

//IniConfig describes the data structure found config.yml file
type IniConfig struct {
	Urls []Endpoints `yaml:"urls,omitempty"` //Urls configured for the Gateway
	Port int         `yaml:"port,omitempty"` //Gateway listening port
}

//Endpoints describes the structure of a GatewayIniConfig.Urls object
type Endpoints struct {
	Path   string                 `yaml:"path,omitempty"`   //Path that is available from external calls
	Method string                 `yaml:"method,omitempty"` //HTTP1.1 Request Method
	Nsq    NsqServiceOptions      `yaml:"nsq,omitempty"`    //NSQ Service Options
	HTTP   HTTPRestServiceOptions `yaml:"http,omitempty"`   //Http(s) Options
}

//NsqServiceOptions describes the options for the gateway to interact with NSQ messaging service
type NsqServiceOptions struct {
	Topic    string `yaml:"topic,omitempty"`    //Topic to post messages for async services
	Nsqdhost string `yaml:"nsqdhost,omitempty"` //nsqd host:port that listens to HTTP clients
}

//HTTPRestServiceOptions describes the options for the gateway regarding the HTTP REST APIs
type HTTPRestServiceOptions struct {
	Host string `yaml:"host,omitempty"` //Hostname
}

//CONSTANTS

//ConfigFileName Path of the config file
const ConfigFileName string = "./config.yaml"

//VARIABLES

//Config is the struct that contains all the settings specified in config file
var Config IniConfig

//Extracts the config values from YAML config file
func (conf IniConfig) getConfFromYaml(fileName string) (result IniConfig, err error) {
	yamlFile, err := ioutil.ReadFile(fileName)
	if err != nil {
		log.Printf("yamlFile.Get err   #%v ", err)
		return conf, err
	}
	err = yaml.Unmarshal(yamlFile, &conf)
	if err != nil {
		log.Printf("Unmarshal: %v", err)
		return conf, err
	}
	return conf, nil
}

func init() {
	//Loads config values from YAML file and sets them to Config global variable
	yamlConfig, err := Config.getConfFromYaml(ConfigFileName)
	if err != nil {
		log.Fatalf("Gateway can't be initialized. Exiting  %v", "")
	}
	log.Printf("Config values: %+v\n", yamlConfig)
	//Updates Config global variable
	Config = yamlConfig
}

// setupRouter initializes the routes for the Gateway
func setupRouter() *gin.Engine {
	//Sets up the Gin framework router
	router := gin.Default()
	//Builds the routes dynamically
	for _, endpoint := range Config.Urls {
		var handler func(*gin.Context)
		if endpoint.Nsq.Topic != "" {
			handler = endpoint.Nsq.nsqHandler
		} else if endpoint.HTTP.Host != "" {
			handler = endpoint.HTTP.httpForward
		}
		router.Handle(endpoint.Method, endpoint.Path, handler)
	}
	return router
}

//nsqHandler Saves the payload to a NSQ topic
func (opts NsqServiceOptions) nsqHandler(c *gin.Context) {
	//Extract paramenters from the path
	id := c.Param("id")
	// Extracts the host and topic from opts
	host := opts.Nsqdhost
	topic := opts.Topic
	//Builds the url
	url := "http://" + host + "/pub?topic=" + topic
	//Reads the submitted body
	body, err := ioutil.ReadAll(c.Request.Body)
	if err != nil {
		//Answers with a 400 error
		log.Printf("Client sent a wrongly formatted body. Error in ioutil.ReadAll(c.Request.Body): %v", err)
		c.String(http.StatusBadRequest, "Body is wrongly formatted. %v", err)
		return
	}
	//Parse the body to see if it is legit and correctly formatted
	var parsedBody map[string]interface{}
	err = json.Unmarshal(body, &parsedBody)
	if err != nil {
		//Not a JSON
		log.Printf("Body: %v", string(body))
		log.Printf("Error while Unmarshaling body into JSON. %v", err)
		c.String(http.StatusBadRequest, "Body is not a JSON.")
		return
	}
	//Body is a JSON. Has it the necessary fields?
	longitude, isThereLongitude := parsedBody["longitude"]
	latitude, isThereLatitude := parsedBody["latitude"]
	if !isThereLongitude || !isThereLatitude {
		//Missing fields
		c.String(http.StatusBadRequest, "Missing latitude/longitude")
		return
	}
	//Fields are there. Check if they are correctly typed
	var location [2]float64 //[long, lat]
	switch v := longitude.(type) {
	case float64:
		location[0] = v
	default:
		c.String(http.StatusBadRequest, "Longitude is not a number")
		return
	}
	switch v := latitude.(type) {
	case float64:
		location[1] = v
	default:
		c.String(http.StatusBadRequest, "Latitude is not a number")
		return
	}
	//Builds a message payload for NSQ service
	messagePayload := map[string]interface{}{
		"driverId":  id,
		"longitude": location[0],
		"latitude":  location[1],
	}
	//Builds the JSON message payload
	message, err := json.MarshalIndent(messagePayload, "", "  ")
	if err != nil {
		log.Printf("Error in encoding json for NSQ service. Error: %v", err)
		c.String(http.StatusInternalServerError, "Ooops. Something went wrong on our side.")
		return
	}
	// Posts the message to NSQ service
	log.Printf("POSTing to NSQ service: %s", string(message))
	resp, err := http.Post(url, "application/json", bytes.NewBuffer(message))
	if err != nil {
		log.Printf("Error in POSTing topic to NSQ service. Error: %v", err)
		c.String(http.StatusBadGateway, "Ooops. Something went wrong on our side.")
		return
	}
	defer resp.Body.Close()
	respBody, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		log.Printf("Problem in processing the response from NSQ service. Error returned %v", err)
		c.String(http.StatusBadGateway, "Ooops. Something went wrong on our side.")
		return
	}
	log.Printf("Answer from NSQ: %s", string(respBody))
	c.String(http.StatusOK, "%v", "Got data!")
}

//httpForward Forwards the request to an external host (upstream) and gives back its answer to the requesting client
func (opts HTTPRestServiceOptions) httpForward(c *gin.Context) {
	id := c.Param("id")
	host := opts.Host
	log.Printf("ID: %v - Host %s", id, host)
	resp, err := http.Get("http://" + host + "/drivers/" + id)
	if err != nil {
		log.Printf("We had a problem in forwarding your request to our systems. Error returned %s", err)
		c.String(http.StatusBadGateway, "We had a problem in forwarding your request to our systems.")
	} else {
		defer resp.Body.Close()
		body, err := ioutil.ReadAll(resp.Body)
		if err != nil {
			log.Printf("We had a problem in processing the response. Error returned %v", err)
			c.String(http.StatusBadGateway, "We had a problem in processing the response.")
		} else {
			c.String(resp.StatusCode, "%s", string(body))
		}
	}
}

func main() {
	//Sets the routes
	router := setupRouter()
	//Starts the gateway
	if Config.Port == 0 {
		router.Run()
	} else {
		router.Run(":" + strconv.Itoa(Config.Port))
	}
}
