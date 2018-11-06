/*
Zombie-service for Zombie test.
Tells if a driver is a zombie or not

*/
package main

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gomodule/redigo/redis"
	yaml "gopkg.in/yaml.v2"
)

//IniConfig describes the data structure found config.yml file
type IniConfig struct {
	Port                  int                 `yaml:"port,omitempty"`                    //Gateway listening port
	Redis                 RedisServiceOptions `yaml:"redis,omitempty"`                   //Redis options
	DriverLocationService DLSOptions          `yaml:"driver-location-service,omitempty"` //Driver location service options
}

//RedisServiceOptions describes the options for Redis service
type RedisServiceOptions struct {
	Host     string `yaml:"host,omitempty"`     //host:port to connect Redis clients
	Password string `yaml:"password,omitempty"` //Password for AUTH command
}

//DLSOptions describes the options for the gateway regarding the Driver-Location-Service REST APIs
type DLSOptions struct {
	Host string `yaml:"host,omitempty"` //Hostname
}

//GLOBAL CONSTANTS

//ConfigFileName Path of the config file
const ConfigFileName string = "./config.yaml"

//DefaultMins Default minutes given by getLocations
const DefaultMins = 5

//ChannelName Default NSQ channel name
const ChannelName = "driver-location-service"

const (
	//ZombieElapse is the time (in minutes) in which a zombie can cover a maximum distance distance = ZombieMaxDistanceCovered
	ZombieElapse float64 = 5
	//ZombieMaxDistanceCovered is the max distance (in meters) that a zombie can cover in ZombieElapse time
	ZombieMaxDistanceCovered float64 = 500
	//ZEKey Redis key to set ZombieElapse
	ZEKey = "zombie-e"
	//ZMDCKey Redis key to set ZombieMaxDistanceCovered
	ZMDCKey = "zombie-mdc"
)

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

func getZombieParams() (ze, zmdc float64) {
	//Gets a connection from the Redis connection pool
	if pool == nil {
		//Pool not initialized (e.g. in unit tests). Provide a pool initialization
		pool = newPool(Config.Redis.Host)
	}
	conn := pool.Get()
	defer conn.Close()
	//Retrieves dynamic zombie-definition parameters (if they exists)
	ze, err := redis.Float64(conn.Do("GET", ZEKey))
	if err != nil {
		if err == redis.ErrNil {
			//nil value. Use default
			log.Printf("nil value for %v key in Redis", ZEKey)
		} else {
			//Something went wrong
			log.Printf("An error occurred during Redis GET of ZombieElapse. %v", err)
		}
		//Assign a default value
		ze = ZombieElapse
	}
	zmdc, err = redis.Float64(conn.Do("GET", ZMDCKey))
	if err != nil {
		if err == redis.ErrNil {
			//nil value. Use default
			log.Printf("nil value for %v key in Redis", ZMDCKey)
		} else {
			//Something went wrong
			log.Printf("An error occurred during Redis GET of ZombieElapse. %v", err)
		}
		//Assign a default value
		zmdc = ZombieMaxDistanceCovered
	}
	return ze, zmdc
}

func evaluateDistance(parsedBody []map[string]interface{}, id string) (float64, error) {
	//Sets cumulativeDistance initial value = 0
	var cumulativeDistance float64
	//Gets a connection from the Redis connection pool
	if pool == nil {
		//Pool not initialized (e.g. in unit tests). Provide a pool initialization
		pool = newPool(Config.Redis.Host)
	}
	conn := pool.Get()
	defer conn.Close()
	//Extracts the timestamps in Unix format from all the JSONs in parsedBody
	tsList := make([]int64, 0)
	for i, jsonEntry := range parsedBody {
		//Checks that the timestamp is there
		v, isThere := jsonEntry["updated_at"]
		if !isThere {
			log.Printf("updated_at field is not in JSON object at index %v", i)
		} else {
			//timestamp is there
			//Type assertion
			ISOts, ok := v.(string)
			if !ok {
				//Not a string
				log.Printf("updated_at field is not a string in JSON object at index %v", i)
			} else {
				t, err := time.Parse(time.RFC3339, ISOts)
				if err != nil {
					//Something went wrong in time conversion
					log.Printf("Error in parsing timestamp from JSON. %v", err)
				} else {
					//Convert Go timestamp in Unix timestamp and add it to timestamps list
					ts := t.Unix()
					tsList = append(tsList, ts)
				}
			}
		}
	}
	log.Printf("List of eligible timestamps retrieved for driver ID %v : %v", id, tsList)
	for j, ts := range tsList {
		var (
			delta float64
			err   error
		)
		if j == 0 {
			delta = 0
		} else {
			//Retrieves delta
			delta, err = redis.Float64(conn.Do("GEODIST", fmt.Sprintf("driver:%v:log", id), ts, tsList[j-1], "m"))
			if err != nil {
				//If GEODIST fails, is not possible to evaluate distance. Exits with an error
				log.Printf("An error occurred in evaluateDistance while calling GEODIST. Exiting evaluateDistance with distance = 0,err. %v ", err)
				return 0, err
			}
		}
		//Updates cumulative distance (if GEODIST calls gives an error, delta = 0. We'll rise an exception)
		cumulativeDistance = cumulativeDistance + delta
	}
	//Distance is cumulativeDistance
	log.Printf("Computed cumulativeDistance: %v", cumulativeDistance)
	return cumulativeDistance, nil
}

//isZombie Tells if driver id is a zombie
func isZombie(id string) (brainHungry bool, statusCode int) {
	//By default, a driver is NOT a zombie!
	brainHungry = false
	//Retrieves parameters to define what is a zombie
	ze, zmdc := getZombieParams()
	log.Printf("Params for evaluating zombie status: %v min, %v m", ze, zmdc)
	//Gets positions (and total distances if possible) from driver-location service
	elapsedTime := strconv.FormatFloat(ze, 'f', -1, 64)
	url := fmt.Sprintf("http://%v/drivers/%v/locations?minutes=%v&distance=true", Config.DriverLocationService.Host, id, elapsedTime)
	resp, err := http.Get(url)
	if err != nil {
		//Something went wrong, just exit with default values
		log.Printf("Error in contacting driver-location. %v", err)
		return false, http.StatusServiceUnavailable
	}
	//Manage the 404 (driver doesn't exists) and similar errors
	if resp.StatusCode != 200 {
		log.Printf("Driver-location service answered with a status code != 200: %v", resp.StatusCode)
		return brainHungry, resp.StatusCode
	}
	//Parse the response body
	defer resp.Body.Close()
	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		log.Printf("We had a problem in processing the response from driver-location-service. Error returned %v", err)
		return brainHungry, http.StatusInternalServerError
	}
	//JSON unmarshall (The answer coming from the service is a JSON array)
	parsedBody := make([]map[string]interface{}, 0)
	err = json.Unmarshal(body, &parsedBody)
	if err != nil {
		log.Printf("Something went wrong while decoding the JSON body payload. %v", err)
		return brainHungry, http.StatusInternalServerError
	}
	log.Printf("Parsed body: %v", parsedBody)
	log.Printf("Number of elements: %v", len(parsedBody))
	//Check if cumulativeDistance is there in the last element of the array (more recent one)
	var distance float64
	if len(parsedBody) == 0 {
		//Empty array? Distance = 0
		distance = 0
	} else {
		//Examine the array content
		v, isThere := parsedBody[len(parsedBody)-1]["cumulativeDistance"]
		if isThere {
			//Type assertion of cumulativeDistance value (should be a float64)
			f, ok := v.(float64)
			if ok {
				//Assertion went good. Assign its value to distance
				log.Printf("Distance is a float64. %v", f)
				distance = f
			} else {
				//Assertion went bad. It's needed to evaluate distance
				log.Printf("cumulativeDistance is not a valid value. Call evaluateDistance")
				distance, err = evaluateDistance(parsedBody, id)
				if err != nil {
					//EvaluateDistance failed. Return a default FALSE value + 500 status (not a zombie unless proved the contrary)
					return false, http.StatusInternalServerError
				}
			}
		} else {
			//Value is not there. Evaluating distance
			log.Println("cumulativeDistance field not found in the response from driver-location-service. Evaluate distance")
			distance, err = evaluateDistance(parsedBody, id)
			if err != nil {
				//EvaluateDistance failed. Return a default FALSE value + 500 status (not a zombie unless proved the contrary)
				return false, http.StatusInternalServerError
			}
		}
	}
	//Checks distance against max distance zombie parameter
	if distance <= zmdc {
		brainHungry = true
	}
	return brainHungry, http.StatusOK
}

//zombieDetector Handler for zombie-service endpoint
func zombieDetector(c *gin.Context) {
	//Reads driverId from the path params
	id := c.Param("id")
	//Builds the response
	response := make(map[string]interface{}, 0)
	zombie, statusCode := isZombie(id)
	if statusCode != 200 {
		//Something went bad with the zombie evaluation
		if statusCode == 404 {
			//Driver not found
			response = map[string]interface{}{
				"id":      id,
				"message": "Driver not found",
			}
		} else {
			response = map[string]interface{}{
				"id":      id,
				"message": "An error occurred",
			}
		}
	} else {
		//Everything went good. Prepare the response
		response = map[string]interface{}{
			"id":     id,
			"zombie": zombie,
		}
	}
	//Sends the response
	c.IndentedJSON(statusCode, response)
}

//setupRouter Defines the routes exposed by zombie-dirver service
func setupRouter() *gin.Engine {
	router := gin.Default()
	router.GET("/drivers/:id", zombieDetector)
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

func main() {
	//Creates a Redis pool and sets it to a module wide variable
	pool = newPool(Config.Redis.Host)
	log.Printf("Redis pool stats: %v", pool.Stats())
	//Sets up the Gin framework router in a separate goroutine
	wg.Add(1) //Adds 1 to waitgroup
	go routing()
	log.Println("End of main() calls")
	//Avoids that main() ends with goroutines still in execution
	wg.Wait()
}
