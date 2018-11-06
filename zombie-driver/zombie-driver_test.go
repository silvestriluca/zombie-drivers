/*
Zombie-service for Zombie test.
Tells if a driver is a zombie or not

*/

package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

//saveTestDriverData Saves data for a testing driver
func saveTestDriverData(long, lat float64, timestamp int64, testDriverID string) error {
	//Prepares db entries in Redis
	pool = newPool(Config.Redis.Host)
	conn := pool.Get()
	defer conn.Close()
	_, err := conn.Do("GEOADD", "on-course", long, lat, testDriverID)
	if err != nil {
		return fmt.Errorf("An error occurred while test was interacting with REDIS(GEOADD). %v", err)
	}
	_, err = conn.Do("GEOADD", fmt.Sprintf("driver:%v:log", testDriverID), long, lat, timestamp)
	if err != nil {
		return fmt.Errorf("An error occurred while test was interacting with REDIS(GEOADD). %v", err)
	}
	_, err = conn.Do("SADD", fmt.Sprintf("driver:%v:timestamps", testDriverID), timestamp)
	if err != nil {
		return fmt.Errorf("An error occurred while test was interacting with REDIS(SADD). %v", err)
	}
	return nil
}

func TestZombieDetectorRoute(t *testing.T) {

	//Prepares data for driver test001
	errRedis := saveTestDriverData(2.364988, 48.864193, time.Now().Unix(), "test001")
	if errRedis != nil {
		t.Error(errRedis)
		return
	}

	tests := []struct {
		name         string
		driverID     string
		expectedCode int
		zombie       bool
	}{
		//Test Cases
		{"Test driver is a zombie", "test001", http.StatusOK, true},
		{"Not existing test driver", "IDONTEXIST", http.StatusNotFound, false},
	}
	for _, tt := range tests {
		router := setupRouter()
		w := httptest.NewRecorder()
		req, _ := http.NewRequest("GET", "/drivers/"+tt.driverID, nil)
		router.ServeHTTP(w, req)
		assert.Equal(t, tt.expectedCode, w.Code, "Testing "+tt.name)
		assert.Contains(t, w.Body.String(), tt.driverID, "Testing "+tt.name)
		if w.Code == 404 {
			assert.Contains(t, w.Body.String(), fmt.Sprintf("\"id\": \"%v\"", tt.driverID))
			assert.Contains(t, w.Body.String(), "\"message\": \"Driver not found\"")
		} else {
			assert.Contains(t, w.Body.String(), strconv.FormatBool(tt.zombie), "Testing "+tt.name)
		}
	}
}

func Test_evaluateDistance(t *testing.T) {
	driverID := "test002"
	//Prepares data for driver test002
	ts := time.Now()
	now := ts.Unix()
	nowISO := ts.UTC().Format(time.RFC3339)
	before := ts.Unix() - 30
	beforeISO := time.Unix(before, 0).UTC().Format(time.RFC3339)
	errRedis := saveTestDriverData(2.365988, 48.864193, before, driverID)
	if errRedis != nil {
		t.Error(errRedis)
		return
	}
	errRedis = saveTestDriverData(2.364988, 48.864193, now, driverID)
	if errRedis != nil {
		t.Error(errRedis)
		return
	}

	parsedBody := make([]map[string]interface{}, 2)
	type args struct {
		parsedBody []map[string]interface{}
		id         string
	}
	tests := []struct {
		name                string
		args                args
		parsedBodyVariation map[string]interface{}
		want                float64
		wantErr             bool
	}{
		// TODO: Add test cases.
		{"Distance computation", args{parsedBody, driverID}, map[string]interface{}{"variation": false}, 73.4, false},
		{"Missing data", args{parsedBody, driverID}, map[string]interface{}{"longitude": 33}, 0, false},
		{"Not existing driver", args{parsedBody, "IDONTEXIST"}, map[string]interface{}{"variation": false}, 0, true},
		{"updated_at not a string", args{parsedBody, driverID}, map[string]interface{}{"longitude": 2.364988, "latitude": 48.864193, "updated_at": 153232}, 0, false},
	}
	for _, tt := range tests {
		parsedBody[0] = map[string]interface{}{
			"longitude":  2.365988,
			"latitude":   48.864193,
			"updated_at": beforeISO,
		}
		parsedBody[1] = map[string]interface{}{
			"longitude":  2.364988,
			"latitude":   48.864193,
			"updated_at": nowISO,
		}
		t.Run(tt.name, func(t *testing.T) {
			_, ok := tt.parsedBodyVariation["longitude"]
			if ok {
				tt.args.parsedBody[1] = tt.parsedBodyVariation
			}
			got, err := evaluateDistance(tt.args.parsedBody, tt.args.id)
			if (err != nil) != tt.wantErr {
				t.Errorf("evaluateDistance() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("evaluateDistance() = %v, want %v", got, tt.want)
			}
		})
	}
}
