/*
Driver location service for Zombie test.

*/
package main

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"math"
	"net/http"
	"reflect"
	"strconv"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gomodule/redigo/redis"
	nsq "github.com/nsqio/go-nsq"
	yaml "gopkg.in/yaml.v2"
)

//IniConfig describes the data structure found config.yml file
type IniConfig struct {
	Port  int                 `yaml:"port,omitempty"`  //Gateway listening port
	Redis RedisServiceOptions `yaml:"redis,omitempty"` //Redis options
	Nsq   NsqServiceOptions   `yaml:"nsq,omitempty"`   //Nsq options
}

//RedisServiceOptions describes the options for Redis service
type RedisServiceOptions struct {
	Host     string `yaml:"host,omitempty"`     //host:port to connect Redis clients
	Password string `yaml:"password,omitempty"` //Password for AUTH command
}

//NsqServiceOptions describes the options for the driver-location service to interact with NSQ messaging service
type NsqServiceOptions struct {
	NsqlookupdHost string `yaml:"nsqlookupd-host,omitempty"` //nsqlookupd host:port that listens to native clients
	Topic          string `yaml:"topic,omitempty"`           //Topic to look for messages
	ChannelName    string `yaml:"channel,omitempty"`         //Channel name assigned to service's consumer
	MaxInflight    int    `yaml:"max-inflight,omitempty"`    //Maximum number of messages to allow in flight (concurrency knob)
	NumPublishers  int    `yaml:"num-publishers,omitempty"`  //number of concurrent publishers
}

//GLOBAL CONSTANTS

//ConfigFileName Path of the config file
const ConfigFileName string = "./config.yaml"

//DefaultMins Default minutes given by getLocations
const DefaultMins float64 = 5

//ChannelName Default NSQ channel name
const ChannelName = "driver-location-service"

//MaxReturnedElements Specifies how many elements can be returned in a Redis SORT request
//DEPRECATED - Evaluated dynamically according to the requested time length
//const MaxReturnedElements = 100

//GLOBAL VARIABLES

//Config is the struct that contains all the settings specified in config file
var Config IniConfig
var (
	pool *redis.Pool    //redis connection pool
	wg   sync.WaitGroup //Global waitgroup
)

func init() {
	//Loads config values from YAML file and sets them to Config global variable
	yamlConfig, err := Config.getConfFromYaml(ConfigFileName)
	if err != nil {
		log.Fatalf("Driver-location service can't be initialized. Exiting  %v", "")
	}
	log.Printf("Config values: %+v\n", yamlConfig)
	//Updates Config global variable
	Config = yamlConfig
	//TODO: Checks for minimal informations in config file and provide default values

}

//getConfFromYaml Extracts the config values from YAML config file
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

func timestampAsISO(ts int64) (ISOstring string) {
	t := time.Unix(ts, 0)
	JavascriptISOString := "2006-01-02T15:04:05Z07:00" //same as time.RFC3339
	ISOstring = t.UTC().Format(JavascriptISOString)
	return ISOstring
}

//newPool Creates a new Redis connection pool
func newPool(addr string) *redis.Pool {
	p := &redis.Pool{
		MaxIdle:     3,
		IdleTimeout: 240 * time.Second,
		Dial: func() (redis.Conn, error) {
			/*c, err := redis.Dial("tcp", addr)
			if err != nil {
				log.Printf("Error in connecting to Redis: %v", err)
				return nil, err
			}
			log.Printf("Connected!")
			return c, nil
			*/
			return redis.Dial("tcp", addr)
		},
	}
	return p
}

//getLocations Replies with an array of driver locations in the requested timespan
func getLocations(c *gin.Context) {
	//Reads minutes from querystring
	var minutes string
	minutes = c.DefaultQuery("minutes", "5")
	min, err := strconv.ParseFloat(minutes, 64)
	if err != nil {
		//Sets the default values in case of erroneous input
		min = DefaultMins
	}
	//Reads the distance flag from the querystring
	var distanceReq string
	wantsDistance := false //By default the distance computation is not required
	distanceReq = c.DefaultQuery("distance", "false")
	if distanceReq == "true" {
		wantsDistance = true
	}
	//Reads driverId from the path params
	id := c.Param("id")
	//Retrieves the eligible timestamps info from REDIS
	//Evaluate Now() timestamp (Unix time)
	now := time.Now().Unix()
	//Gets a list of recorded timestamps for driver:id
	if pool == nil {
		//Pool not initialized (e.g. in unit tests). Provide a pool initialization
		log.Println("Redis pool not initialized. Proceed with initialization")
		pool = newPool(Config.Redis.Host)
	}
	conn := pool.Get()
	defer conn.Close()
	reply, err := redis.Int64s(conn.Do("SORT", fmt.Sprintf("driver:%v:timestamps", id), "LIMIT", 0, min*60, "DESC"))
	if err != nil {
		log.Printf("Error in processing SORT request. %v", err)
		c.String(http.StatusInternalServerError, "Ooops. Something went wrong on our side.")
		return
	}
	log.Printf("Got timestamp list. %v", reply)
	//Empty timestamp list? --> driver doesn't exist. Reply with 404 error
	if len(reply) == 0 {
		notFoundReply := map[string]string{
			"message": "Driver not found",
		}
		c.IndentedJSON(http.StatusNotFound, notFoundReply)
		return
	}
	eligibleTimestamps := make([]int64, 0)
	for i := 0; i < len(reply); i++ {
		timestamp := reply[i]
		if float64(timestamp) >= float64(now)-(min*60) {
			eligibleTimestamps = append(eligibleTimestamps, reply[i])
		} else {
			//Sorted array. There are no more interesting timestamps. Ends for cycle
			i = len(reply)
		}
	}
	//Builds the response
	response := make([]map[string]interface{}, 0)
	var total float64 //total Holds the total distance that a driver made during "minutes"
	total = 0
	for i := 0; i < len(eligibleTimestamps); i++ {
		//Since eligibleTimestamps is ordered by DESC and the response is ordered by ASC we use a decreasing cursor
		j := len(eligibleTimestamps) - 1 - i
		timestamp := eligibleTimestamps[j]
		reply, err := redis.Positions(conn.Do("GEOPOS", fmt.Sprintf("driver:%v:log", id), timestamp))
		if err != nil {
			//Error in executing GEOPOS. Go on with next i
			log.Printf("Error in processing GEOPOS request. %v", err)
			log.Println("Going on to retrieve GEOPOS for other eligible timestamps")
		} else {
			//Reply contains long and lat. Add them to response and round to 6 digits precision
			newElement := make(map[string]interface{})
			newElement["latitude"] = math.Floor(reply[0][1]*1e6) / 1e6
			newElement["longitude"] = math.Floor(reply[0][0]*1e6) / 1e6
			newElement["updated_at"] = timestampAsISO(timestamp)
			//Check if it has to add distance and delta in the response
			if wantsDistance {
				//Evaluates the distance between eligibleTimestamps[j] and eligibleTimestamps[j-1]
				var delta float64
				if i == 0 {
					//The first element is the start for evaluating deltas
					delta = 0
				} else {
					//Retrieves delta
					delta, err = redis.Float64(conn.Do("GEODIST", fmt.Sprintf("driver:%v:log", id), eligibleTimestamps[j], eligibleTimestamps[j+1], "m"))
				}
				//Updates total (if GEODIST calls gives an error, delta = 0)
				total = total + delta
				//Adds delta and total to the response
				newElement["elapsedDistance"] = math.Floor(delta*1e3) / 1e3
				newElement["cumulativeDistance"] = math.Floor(total*1e3) / 1e3
			}
			//Updates response slice
			response = append(response, newElement)
		}
	}
	//Sends the response
	c.IndentedJSON(http.StatusOK, response)
	return
}

func validateInput(input map[string]interface{}) bool {
	//At the beginning the return value isValidated is set to "true"
	isValidated := true
	//1. Are there the necessary fields?
	longitude, isThereLongitude := input["longitude"]
	latitude, isThereLatitude := input["latitude"]
	id, isThereID := input["driverId"]
	if !isThereLongitude || !isThereLatitude || !isThereID {
		//Missing fields
		log.Println("Missing fields")
		isValidated = false
		//Returns immediately to avoid missing fields parsing attempts
		return isValidated
	}
	//2. Checks if the fields are ther with nil values
	if id == nil || latitude == nil || longitude == nil {
		log.Println("Some nil values")
		isValidated = false
		//Returns immediately to avoid nil fields parsing attempts
		return isValidated
	}
	//3. Fields are there. Check if they are correctly typed
	//long/lat
	if reflect.TypeOf(longitude).String() != "float64" || reflect.TypeOf(latitude).String() != "float64" {
		log.Println("Wrong numeric type")
		log.Printf("long: %v, lat: %v", reflect.TypeOf(longitude).String(), reflect.TypeOf(latitude).String())
		isValidated = false
		return isValidated
	}
	//id
	if !(reflect.TypeOf(id).String() == "string" || reflect.TypeOf(id).String() == "float64" || reflect.TypeOf(id).String() == "int") {
		//for our use, id could be a string or a number  -> json.unmarshall parse numbers as float64. Int is kept for future possibilities
		log.Println("Wrong id type")
		isValidated = false
		return isValidated
	}
	//4. Check the emptyness/in range values only if the validation is true so far
	if isValidated {
		//Checks if the fields are not empty or have invalid values
		if longitude.(float64) > 180 || longitude.(float64) < -180 || latitude.(float64) > 85.05112878 || latitude.(float64) < -85.05112878 || id == "" {
			log.Println("Invalid value")
			isValidated = false
			return isValidated
		}
	}
	return isValidated
}

//persistMessageToRedis Saves a valid message to an appropriate set of key-values in Redis
func persistMessageToRedis(message map[string]interface{}) error {
	//Gets a connection from the Pool
	if pool == nil {
		//Pool not initialized (e.g. in tests). Create a new pool
		pool = newPool(Config.Redis.Host)
	}
	conn := pool.Get()
	defer conn.Close()
	//Checks if there have been problems in connecting
	if conn.Err() != nil {
		log.Printf("Error in connecting to Redis: %v", conn.Err())
		return conn.Err()
	}
	//Saves the instant position
	writeErrors := make([]error, 0)
	timestamp := message["timestamp"].(int64) //timestamp in Unix time
	latitude := message["latitude"].(float64)
	longitude := message["longitude"].(float64)
	id := message["driverId"]
	//On-course key. Adds the position of driver id
	_, err := conn.Do("GEOADD", "on-course", longitude, latitude, id)
	if err != nil {
		writeErrors = append(writeErrors, err)
		log.Printf("Error in saving on-course data with GEOADD: %v", err)
	}
	//driver:id:log key. Adds postion & timestamp
	_, err = conn.Do("GEOADD", fmt.Sprintf("driver:%v:log", id), longitude, latitude, timestamp)
	if err != nil {
		writeErrors = append(writeErrors, err)
		log.Printf("Error in saving driver log data with GEOADD: %v", err)
	}
	//dirver:id:timestamps. Adds the recorded timestamp to a list connected to driver:id
	_, err = conn.Do("SADD", fmt.Sprintf("driver:%v:timestamps", id), timestamp)
	if err != nil {
		writeErrors = append(writeErrors, err)
		log.Printf("Error in saving driver timestamp data with SADD: %v", err)
	}
	//Check if there have been errors in redis writes
	if len(writeErrors) > 0 {
		finalError := fmt.Errorf("There have been errors in redis writes: %v", writeErrors)
		return finalError
	}
	//Exits the method with no errors
	log.Println("Message have been persisted successfully to Redis")
	return nil
}

//handleMessage Handles what to do when a message from NSQ topic/channel is received
func handleMessage(m *nsq.Message) error {
	//log.Printf("Message received: %+v", *m)
	log.Printf("Message body: %v", string(m.Body))
	//Extracts the timestamp in Unix format
	timestamp := m.Timestamp / 1e9
	//JSON is already validated by downstream service (Gateway). Unmarshal it in a generic map
	var parsedMessage map[string]interface{}
	err := json.Unmarshal(m.Body, &parsedMessage)
	if err != nil {
		//Wrong JSON decoding. Return nil to avoid requeuing.
		log.Printf("Something went wrong while decoding the JSON message payload. %v", err)
		return nil
	}
	//Validate the input (message)
	if !validateInput(parsedMessage) {
		//Message hasn't valid format. Return nil to avoid requeuing.
		log.Println("Message has not a valid structure and won't be persisted")
		return nil
	}
	//Input is validated. Add timestamp and send it to Redis
	parsedMessage["timestamp"] = timestamp
	err = persistMessageToRedis(parsedMessage)
	if err != nil {
		//Logs the error but doesn't return an error to the handler (fails silently and avoid requeing)
		log.Printf("An error occured while calling persistMessageToRedis: %v", err)
	}
	return nil
}

//poolNSQForMessages Pools messages from NSQ service
func poolNSQForMessages() {
	cfg := nsq.NewConfig()
	cfg.DialTimeout = 10 * time.Second
	cfg.UserAgent = fmt.Sprintf("driver-location/%s go-nsq/%s", "0.1", nsq.VERSION)
	cfg.MaxInFlight = Config.Nsq.MaxInflight
	consumer, err := nsq.NewConsumer(Config.Nsq.Topic, Config.Nsq.ChannelName, cfg)
	if err != nil {
		log.Fatalf("A problem occurred in initializing NSQ Consumer: %v", err)
		return
	}
	consumer.AddConcurrentHandlers(nsq.HandlerFunc(handleMessage), Config.Nsq.NumPublishers)
	nsqlookupAddresses := make([]string, 0)
	nsqlookupAddresses = append(nsqlookupAddresses, Config.Nsq.NsqlookupdHost)
	err = consumer.ConnectToNSQLookupds(nsqlookupAddresses)
	if err != nil {
		log.Printf("A problem occured in connecting to nsqlookupd: %v", err)
		return
	}
	log.Println("Connected to nsdlookupd")
}

//setupRouter Defines the routes exposed by driver-location service
func setupRouter() *gin.Engine {
	router := gin.Default()
	router.GET("/drivers/:id/locations", getLocations)
	return router
}

// routing Sets up the Gin framework router
func routing() {
	//Sets the routes
	router := setupRouter()
	//Starts the gateway
	if Config.Port == 0 {
		router.Run()
	} else {
		router.Run(":" + strconv.Itoa(Config.Port))
	}
}

//Main routine
func main() {
	//Creates a Redis pool and sets it to a module wide variable
	pool = newPool(Config.Redis.Host)
	log.Printf("Redis pool stats: %v", pool.Stats())
	//Starts to pool NSQ for location messages
	poolNSQForMessages()
	//Sets up the Gin framework router in a separate goroutine
	wg.Add(1) //Adds 1 to waitgroup
	go routing()
	log.Println("End of main() calls")
	//Avoids that main() ends with goroutines still in execution
	wg.Wait()
}
