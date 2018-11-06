# What is this?

**It is a nice coding test that I've taken recently.**

> "Zombie apocalypse is upon us. You can't take a ride safely anymore."

This is a sets of cloud-based microservices that consume and persist geolocated data sent by a mobile device to discover if the owner (a taxi driver) is a zombie or not. 

> A driver is a zombie if he has driven less than 500 meters in the last 5 minutes.

## What are those services? 
### 1. Gateway Service

The `Gateway` service is a _public facing service_.
HTTP requests hitting this service are either transformed into [NSQ](https://github.com/nsqio/nsq) messages or forwarded via HTTP to specific services.

The service must be configurable dynamically by loading the provided `gateway/config.yaml` file to register endpoints during its initialization.

#### Public Endpoints

`PATCH /drivers/:id/locations`

**Payload**

```json
{
  "latitude": 48.864193,
  "longitude": 2.350498
}
```

**Role:**

During a typical day, thousands of drivers send their coordinates every 5 seconds to this endpoint.

**Behaviour**

Coordinates received on this endpoint are converted to [NSQ](https://github.com/nsqio/nsq) messages listened by the `Driver Location` service.

---

`GET /drivers/:id`

**Response**

```json
{
  "id": 42,
  "zombie": true
}
```

**Role:**

Users request this endpoint to know if a driver is a zombie.
A driver is a zombie if he has driven less than 500 meters in the last 5 minutes.

**Behaviour**

This endpoint forwards the HTTP request to the `Zombie Driver` service.

### 2. Driver Location Service
The `Driver Location` service is a microservice that consumes drivers' location messages published by the `Gateway` service and stores them in a Redis database.

It also provides an internal endpoint that allows other services to retrieve the drivers' locations, filtered and sorted by their addition date

#### Internal Endpoint

`GET /drivers/:id/locations?minutes=5`

**Response**

```json
[
  {
    "latitude": 48.864193,
    "longitude": 2.350498,
    "updated_at": "2018-04-05T22:36:16Z"
  },
  {
    "latitude": 48.863921,
    "longitude":  2.349211,
    "updated_at": "2018-04-05T22:36:21Z"
  }
]
```

**Role:**

This endpoint is called by the `Zombie Driver` service.

**Behaviour**

For a given driver, returns all the locations from the last 5 minutes (given `minutes=5`).


### 3. Zombie Driver Service
The `Zombie Driver` service is a microservice that determines if a driver is a zombie or not according to the previously stated definition.

#### Internal Endpoint

`GET /drivers/:id`

**Response**

```
{
  "id": 42,
  "zombie": true
}
```

**Role:**

This endpoint is called by the `Gateway` service.

**Behaviour**

Returns the zombie state of a given driver. 


# Setting up 
## Premises  
### Environment:
The following assumptions has been made for testing environment
- The Services use the "0" database in Redis 
- AUTH is disabled (no password set)

## How to install it

### 1. Clone repository
1) `git clone git@github.com:silvestriluca/zombie-drivers.git`

2) `cd zombie-drivers `

### 2. Starts the NSQ and REDIS services
1) Write down the IP of a machine running docker (e.g. if you use `docker-machine` issue the command `docker-machine ip`)

2) Open the file  `docker-compose.yml`

3) Edit the following entry under `nsqd` service: `command: /nsqd --lookupd-tcp-address=nsqlookupd:4160 --broadcast-address=YOUR-DOCKER-MACHINE-IP-GOES-HERE`

4) Save `docker-compose.yml`

5) In a terminal, navigate to repo root directory and issue the following command to launch Redis and NSQ: `docker-compose up`

6) Open a browser and navigate to `http://YOUR-DOCKER-MACHINE-IP-GOES-HERE:4171/`. Nsqadmin dashboard should launch

7) Check the `NODES` tab. If everything went good, you should see a `Hostname` with `Broadcast address` pointing to `YOUR-DOCKER-MACHINE-IP-GOES-HERE` 
### 3. Build Gateway
From the **root repository directory**:

1) `cd gateway`

2) `go get`  => installs the required packages

3) `go build`  => The executable is built and created in the current directory

4) Edit `config.yaml` file to set the gateway parameters. Choose a port that is not already occupied (e.g. `3000`)
### 4. Build Driver-location
From the **root repository directory**:

1) `cd driver-location`

2) `go get` => installs the required packages

3) `go build` => The executable is built and created in the current directory

4) Edit `config.yaml` file to set the service parameters. Choose a different port than `Gateway service` (e.g. `3001`)

### 5. Build Zombie-driver
From the **root repository directory**:

1) `cd zombie-driver`

2) `go get` => installs the required packages

3) `go build` => The executable is built and created in the current directory

4) Edit `config.yaml` file to set the service parameters. Choose a different port than `Gateway service` and  `Driver location service` (e.g. `3002`)

5) To **dynamically set** "Zombie definitions" save the following keys in Redis "0" database
- `SET zombie-e [value]`  => Timespan (in minutes) to evaluate a zombie state (default = 5 min)

- `SET zombie-mdc [value]`  => Maximum distance (in meters) that a zombie can cover zombie-e timespan (default = 500 m)

## How to launch the services
1) If you've followed the **HOW TO INSTALL** section, you should have 3 executables in 
- `REPOSITORY_DIR/gateway` 
- `REPOSITORY_DIR/driver-location` 
- `REPOSITORY_DIR/zombie-driver`  

2) Be sure that the **executables have exec permissions**.

3) Be sure that REDIS and NSQ services are running. If not, from the REPOSITORY_DIR root directory issue `docker-compose start`

4) Be sure that Redis has no password set (no AUTH)

5) To launch a service, from REPOSITORY_DIR root directory:

 - `cd SERVICENAME`
 - `SERVICENAME`

## Tests
As of now, tests don't mock external services, so they REQUIRE all the services running (gateway, driver-location, zombie-driver, redis, nsq).
Tests can be executed by running in the service directory the command `go test`

Example: From the root REPOSITORY_DIR
- `cd gateway` 
- `go test` 

# Description 
### Environment assumptions (reasonable for a testing environment):
The following assumptions have been made during development and they can be easily removed with little code changes.
- The Demo use the "0" database in Redis 
- AUTH is disabled (no password set. AUTH isn't implemented)
- All the communications happen in the backend and are unencrypted (no TLS). For the Gateway that means that there is a webserver in front of it, like Apache or Nginx configured like a proxy with proper certificates to ensure https on public-facing Rest APIs.
See also Gateway description.

### Gateway
The gateway has endpoints and exposed port fully configurable. Configuration happens by modifying the `config.yaml` file, located in the same directory as the executable. The Config object (struct) holds all the configurations and can be used in the future for different config source (e.g. command line aguments using flag package). At this moment, the path+name of the config file can be changed only by editing the global const `ConfigFileName`.

To manage a scalable routing system the choice felt on GIN (<https://gin-gonic.github.io/gin/>) as it is listed as one of the highest performance router out there. Every endpoint is managed by a concurrent handler.

Gateway is http-only in its current setup . Having a front proxy (Apahe/Nginx) configured with server certificates is a common setup, that is why I've implemented an http-only gateway. Changing this design choice is very easy, since Gin can be quickly configured to serve the endpoints on https.

YAML parsing has been implemented by adopting the widely used <https://gopkg.in/yaml.v2>

Posting to NSQ topic is made using a HTTP POST request to a nsqd http `/pub` endpoint. The call happens in the handler called by GIN route (by default `/drivers/:id/locations`). 
Using a handler ensures scalability (GIN takes care to spawn an underlying goroutine at every handler call).
A possible improvement is using the nsq client and its Producer object, which would guarantee a better topology abstraction by using nsqdlookup directly to discover nsqd servers.

Since the `/drivers/:id/locations` call saves the location informations asyncronously, a preliminary validation on payload (body) is being made and a 400 error is returned if the payload is malformed or lacks informations.

**TESTS** cover most of the code but they rely on working services. An improvement to them could be a full mocking of the other services, but this hasn't been implemented in this initial commit.

All the codebase (service and tests) is fully commented to be easily readable and self-explaining.

### Driver Location Service
The driver-location service is configurable through a `config.yaml` file, located in the same directory as the executable. Settings include listening port, nsq and redis related informations. The Config object (struct) holds all the configurations and can be used in the future for different config source (e.g. command line aguments using flag package). At this moment, the path+name of the config file can be changed only by editing the global const `ConfigFileName`.

To manage a scalable routing system the choice felt on GIN (<https://gin-gonic.github.io/gin/>) for the same reasons stated for the gateway service. Every endpoint is managed by a concurrent handler.

Service endpoints are http-only as it is supposed to run in an isolated backend behind a proxy and gateway. As for the gateway, changing this design choice is very easy, since Gin can be quickly configured to serve the endpoints on https.

YAML parsing has been implemented by adopting the widely used <https://gopkg.in/yaml.v2>

NSQ interaction happens by implementing a Consumer object, as provided by the official nsq go client (<https://github.com/nsqio/go-nsq>). The client pools nsqlookupd to discover nsqds that provides the specified topic.

A (concurrent) handler is attached to receive message event. The number of concurrent publishers can be set in config file (`num-publishers`). Scalability is assured by the concurrent handler approach. Some load tests and number of driver estimations should be done to define the ideal `num-publishers` value, taking in consideration also more driver-location-service instances.

Redis interaction is done by using the well-known Redigo client (<https://github.com/gomodule/redigo>).
A connection pool is initialized at service launch. When a redis related operation is needed, a connections is taken from the pool and sent back to it when operation ends. 

With the code settings there are no limits on the number of connections in the pool while the maximum number of idle connections in the pool is 3, with an idle timeout = 240s

The service makes use of Redis geospatial features (GEOADD)

The Redis data structure is described in the following [section](#data). There it is described also how the service retrieves the locations requested with `GET /drivers/:id/locations?minutes=5`

A validator function takes care of parsing the message input for errors/problems before persisting it to the database.

Requests to the `GET /drivers/:id/locations?minutes=5` endpoint relative to non-existing drivers result in HTTP 404 errors.

Internal errors result in HTTP 500 response.

Errors are managed (as far as I've tested).

For **TESTS**, same considerations as described for Gateway are valid.

All the codebase (service and tests) is fully commented to be easily readable and self-explaining.

**BONUS:** An optional `distance=true` querystring option has been integrated for endpoint `GET /drivers/:id/locations`. Internally it uses GEODIST (as described in [this section](#data)) and allows to recover the elapsedDistance and cumulativeDistance in the last Z minutes timespan. For example:

`GET /drivers/:id/locations?distance=true&minutes=5` 
```
[
    {
        "cumulativeDistance": 0,
        "elapsedDistance": 0,
        "latitude": 48.864193,
        "longitude": 2.364986,
        "updated_at": "2018-10-24T13:58:10Z"
    },
    {
        "cumulativeDistance": 73.4,
        "elapsedDistance": 73.4,
        "latitude": 48.864193,
        "longitude": 2.365989,
        "updated_at": "2018-10-24T13:58:24Z"
    },
    {
        "cumulativeDistance": 146.407,
        "elapsedDistance": 73.007,
        "latitude": 48.864193,
        "longitude": 2.366987,
        "updated_at": "2018-10-24T14:00:17Z"
    }
]
```

### Zombie Driver Service
The zombie-driver service is configurable through a `config.yaml` file, located in the same directory as the executable. Settings include listening port, Redis and Driver-Location-Service related informations. The Config object (struct) holds all the configurations and can be used in the future for different config source (e.g. command line aguments using flag package). At this moment, the path+name of the config file can be changed only by editing the global const `ConfigFileName`.

To manage a scalable routing system the choice felt on GIN (<https://gin-gonic.github.io/gin/>) for the same reasons stated for the gateway service. Every endpoint is managed by a concurrent handler.

Service endpoints are http-only as it is supposed to run in an isolated backend behind a proxy and gateway. As for the gateway, changing this design choice is very easy, since Gin can be quickly configured to serve the endpoints on https.

YAML parsing has been implemented by adopting the widely used <https://gopkg.in/yaml.v2>

The service uses Redigo client (<https://github.com/gomodule/redigo>). Same pool approach than Driver Location Service has been followed.

To evaluate "zombie state", a distance has to be computed. By default it tries to retrive it from Driver-Location Service using `distance=true` querystring option. If the answer doesn't include distance informations, the zombie-driver service computes it using GEODIST Redis function, as described in [this section](#data).

Requests to the `GET /drivers/:id` endpoint relative to non-existing drivers result in HTTP 404 errors.

Internal errors / missing-upstream-service errors result in HTTP 5xx response (500/503).

Errors are managed (as far as I tested).

For **TESTS**, same considerations as described for Gateway are valid.

All the codebase (service and tests) is fully commented to be easily readable and self-explaining.

**BONUS:** "Zombie parameters" (max distance D covered in time Z) default as per initial service description (D=500m , Z=5s) but can be optionally set in Redis.

The zombie definition is configurable on fly through 2 REDIS key-values:
- **zombie-e** => Timespan (in minutes) to evaluate a zombie state (default = 5 min)
- **zombie-mdc** =>  Maximum distance (in meters) that a zombie can cover during zombie-e timespan (default = 500 m) 

## Redis: Driver related data structure and how it is used by the services<a name="data"></a>
Everytime a driver sends his/her location, the following keys are populated:
1) `on-course` => GEOADD longitude, latitude, **driverId**
2) `driver:<driverId>:log` => GEOADD longitude, latitude, **UnixTimestamp**
3) `driver:<driverId>:timestamps` => SADD **UnixTimestamp**

(1) and (2) are geohashes (sorted sets with special methods on it, like GEODIST).

(3) is a set

- (1) stores the last known position of every driver
- (2) stores the position of driverId at a given timestamps
- (3) stores all the recorded timestamps of a given driverId

Recovering  the position lists in the last Z minutes is as easy as:

1) retrieve from (3) the timestamps list with a SORT commmand ordered by DESC (from the newest to the oldest). Limit the search to maximum of Z*60 elements (since Unix timestamp resolution = 1 sec)
2) reduce the list to include only timestamps inside the required timespan
3) Use GEOPOS command on (2) to every timestamp entry in the reduced list 

Evaluating the distance covered by a driverId in a given timespan it's even easier: use the same procedure described before to retrieve relevant timestamps and use GEODIST command to evaluate *delta* distance between two consequent timestamps in the reduced list, iterating on timestamps and cumulating the *deltas*. 

## BONUSES (optional features) :confetti_ball:
### Bonus point 1
The zombie definition is configurable on fly through 2 REDIS key-values:
- **zombie-e** => Timespan (in minutes) to evaluate a zombie state (default = 5 min)
- **zombie-mdc** =>  Maximum distance (in meters) that a zombie can cover during zombie-e timespan (default = 500 m)

### Bonus point 2
The driver-location service accepts an optional  `distance=true` querystring option to recover the elapsedDistance and cumulativeDistance in a given timespan.

Example:

**Request**

`GET /drivers/:id/locations?distance=true&minutes=5`

**Response**
```
[
    {
        "cumulativeDistance": 0,
        "elapsedDistance": 0,
        "latitude": 48.864193,
        "longitude": 2.364986,
        "updated_at": "2018-10-24T13:58:10Z"
    },
    {
        "cumulativeDistance": 73.4,
        "elapsedDistance": 73.4,
        "latitude": 48.864193,
        "longitude": 2.365989,
        "updated_at": "2018-10-24T13:58:24Z"
    },
    {
        "cumulativeDistance": 146.407,
        "elapsedDistance": 73.007,
        "latitude": 48.864193,
        "longitude": 2.366987,
        "updated_at": "2018-10-24T14:00:17Z"
    }
]
```