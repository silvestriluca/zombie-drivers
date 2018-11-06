/*
Driver location service for Zombie test.

*/

package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	nsq "github.com/nsqio/go-nsq"
	"github.com/stretchr/testify/assert"
)

func Test_validateInput(t *testing.T) {
	type args struct {
		input map[string]interface{}
	}
	tests := []struct {
		name string
		args args
		want bool
	}{
		//Test cases
		{"Correct input", args{map[string]interface{}{"driverId": "aaaa", "latitude": 22.000, "longitude": 23.444}}, true},
		{"Correct input with numeric ID", args{map[string]interface{}{"driverId": 3273627, "latitude": 22.000, "longitude": 23.444}}, true},
		{"Correct input  with float64 ID", args{map[string]interface{}{"driverId": 3333.6, "latitude": 22.000, "longitude": 23.444}}, true},
		{"Wrong input with nil ID", args{map[string]interface{}{"driverId": nil, "latitude": 22.000, "longitude": 23.444}}, false},
		{"Wrong input with nil lat/long - lat", args{map[string]interface{}{"driverId": "aaaa", "latitude": nil, "longitude": 23.444}}, false},
		{"Wrong input with nil lat/long - long", args{map[string]interface{}{"driverId": "aaaa", "latitude": 22.000, "longitude": nil}}, false},
		{"Wrong input  with string lat", args{map[string]interface{}{"driverId": "aaaa", "latitude": "22.000", "longitude": 23.444}}, false},
		{"Wrong input with string long", args{map[string]interface{}{"driverId": "aaaa", "latitude": 22.000, "longitude": "23.444"}}, false},
		{"Wrong input with string long/lat", args{map[string]interface{}{"driverId": "aaaa", "latitude": "22.000", "longitude": "23.444"}}, false},
		{"Wrong input with missing fields", args{map[string]interface{}{"driverId": "aaaa", "longitude": 23.444}}, false},
		{"Wrong input with empty fields - id string", args{map[string]interface{}{"driverId": "", "latitude": 22.000, "longitude": 23.444}}, false},
		{"Correct input with zero field - id int", args{map[string]interface{}{"driverId": 0, "latitude": 22.000, "longitude": 23.444}}, true},
		{"Correct input with zero fields - id float64", args{map[string]interface{}{"driverId": 0.000, "latitude": 22.000, "longitude": 23.444}}, true},
		{"Wrong input with lat/long out of scale - lat+", args{map[string]interface{}{"driverId": "aaa", "latitude": 89.00, "longitude": 23.444}}, false},
		{"Wrong input with lat/long out of scale - lat-", args{map[string]interface{}{"driverId": "aaa", "latitude": -89.00, "longitude": 23.444}}, false},
		{"Wrong input with lat/long out of scale - long+", args{map[string]interface{}{"driverId": "aaa", "latitude": 78.00, "longitude": 181.001}}, false},
		{"Wrong input with lat/long out of scale - long-", args{map[string]interface{}{"driverId": "aaa", "latitude": 78.00, "longitude": -181.001}}, false},
		{"Wrong input with lat/long out of scale - lat&long +", args{map[string]interface{}{"driverId": "aaa", "latitude": 91.00, "longitude": 185.001}}, false},
		{"Correct input with negative lat/long", args{map[string]interface{}{"driverId": "aaa", "latitude": -78.00, "longitude": 23.444}}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := validateInput(tt.args.input); got != tt.want {
				t.Errorf("validateInput() = %v, want %v", got, tt.want)
			}
		})
	}
}

func Test_timestampAsISO(t *testing.T) {
	type args struct {
		ts int64
	}
	tests := []struct {
		name          string
		args          args
		wantISOstring string
	}{
		//Test cases
		{"Timestamp in Unix = 1539850371", args{1539850371}, "2018-10-18T08:12:51Z"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if gotISOstring := timestampAsISO(tt.args.ts); gotISOstring != tt.wantISOstring {
				t.Errorf("timestampAsISO() = %v, want %v", gotISOstring, tt.wantISOstring)
			}
		})
	}
}

func Test_handleMessage(t *testing.T) {
	var emptyBody []byte
	var messageID nsq.MessageID
	type args struct {
		m *nsq.Message
	}
	tests := []struct {
		name           string
		decodedMessage map[string]interface{}
		args           args
		wantErr        bool
	}{
		// Test cases.
		{"Regular entry for test001", map[string]interface{}{"longitude": 2.364988, "latitude": 48.864193, "driverId": "test001"}, args{nsq.NewMessage(messageID, emptyBody)}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			byteMessage, _ := json.Marshal(tt.decodedMessage)
			tt.args.m.Body = byteMessage
			if err := handleMessage(tt.args.m); (err != nil) != tt.wantErr {
				t.Errorf("handleMessage() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestGetLocationsRoute(t *testing.T) {
	tests := []struct {
		name             string
		distance         bool
		minutes          string
		driverID         string
		expectedCode     int
		wrongBodyPayload interface{}
	}{
		//Test Cases
		{"5 mins and no distance", false, "6", "test001", http.StatusOK, "[]"},
		{"5 mins and distance", true, "6", "test001", http.StatusOK, "[]"},
		{"aaaa mins (defaults to 5) and distance", true, "aaaa", "test001", http.StatusOK, "[]"},
		{"Not existing driverID", false, "6", "IDONTEXIST", http.StatusNotFound, "[]"},
	}
	for _, tt := range tests {
		router := setupRouter()
		w := httptest.NewRecorder()
		querystring := fmt.Sprintf("distance=%v&minutes=%v", strconv.FormatBool(tt.distance), tt.minutes)
		req, _ := http.NewRequest("GET", "/drivers/"+tt.driverID+"/locations?"+querystring, nil)
		router.ServeHTTP(w, req)
		assert.Equal(t, tt.expectedCode, w.Code, "Testing "+tt.name)
		assert.NotEqual(t, tt.wrongBodyPayload, w.Body.String(), "Testing "+tt.name)
		if tt.distance {
			assert.Contains(t, w.Body.String(), "elapsedDistance")
		} else {
			assert.NotContains(t, w.Body.String(), "elapsedDistance")
		}
		if w.Code == 404 {
			assert.Contains(t, w.Body.String(), "\"message\": \"Driver not found\"")
		}
	}
}
