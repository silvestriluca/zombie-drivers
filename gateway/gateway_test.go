/*
Gateway service for Zombie test.

*/

package main

import (
	"bytes"
	"encoding/json"
	"log"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestIniConfig_getConfFromYaml(t *testing.T) {
	type args struct {
		fileName string
	}
	var config IniConfig
	var expectedConfig IniConfig
	expectedConfig.Urls = []Endpoints{
		{"/drivers/:id/locations", "PATCH", NsqServiceOptions{"locations", "localhost:4151"}, HTTPRestServiceOptions{}},
		{"/drivers/:id", "GET", NsqServiceOptions{}, HTTPRestServiceOptions{"zombie-driver"}},
	}
	expectedConfig.Port = 3000
	var ar args
	ar.fileName = "../test/test-config.yaml"
	tests := []struct {
		name       string
		conf       IniConfig
		args       args
		wantResult IniConfig
		wantErr    bool
	}{
		// Test cases.
		{"Normal parse", config, ar, expectedConfig, false},
		{"Filename error", config, args{"../test/wrong.yml"}, config, true},
		{"File wrongly formatted", config, args{"../test/test-wrong-formatted.yaml"}, config, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotResult, err := tt.conf.getConfFromYaml(tt.args.fileName)
			if err != nil {
				t.Logf("Error detected   #%v ", err)
			}
			if (err != nil) != tt.wantErr {
				t.Errorf("IniConfig.getConfFromYaml() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !reflect.DeepEqual(gotResult, tt.wantResult) {
				t.Errorf("IniConfig.getConfFromYaml() = %v, want %v", gotResult, tt.wantResult)
			}
		})
	}
}

func TestNsqHandlerRoute(t *testing.T) {
	tests := []struct {
		name                string
		bodyAsString        string
		bodyPayload         map[string]interface{}
		expectedCode        int
		expectedBodyPayload interface{}
	}{
		//Test Cases
		{"Regular value", "", map[string]interface{}{"longitude": 2.364988, "latitude": 48.864193}, http.StatusOK, "Got data!"},
		{"Long is a string", "", map[string]interface{}{"longitude": "2.364988", "latitude": 48.864193}, http.StatusBadRequest, "Longitude is not a number"},
		{"Lat is a string", "", map[string]interface{}{"longitude": 2.364988, "latitude": "48.864193"}, http.StatusBadRequest, "Latitude is not a number"},
		{"Missing Long", "", map[string]interface{}{"latitude": 48.864193}, http.StatusBadRequest, "Missing latitude/longitude"},
		{"Missing Lat", "", map[string]interface{}{"longitude": 48.864193}, http.StatusBadRequest, "Missing latitude/longitude"},
		{"Empty body", "", map[string]interface{}{}, http.StatusBadRequest, "Missing latitude/longitude"},
		{"Not a JSON", "ahahaha", map[string]interface{}{}, http.StatusBadRequest, "Body is not a JSON."},
	}
	for _, tt := range tests {
		log.Printf("Testing %v", tt.name)
		router := setupRouter()
		body, _ := json.Marshal(tt.bodyPayload)
		if tt.bodyAsString != "" {
			body = []byte(tt.bodyAsString)
		}
		w := httptest.NewRecorder()
		req, _ := http.NewRequest("PATCH", "/drivers/test001/locations", bytes.NewBuffer(body))
		req.Header.Set("Content-type", "application/json")
		router.ServeHTTP(w, req)
		assert.Equal(t, tt.expectedCode, w.Code, "Testing "+tt.name)
		assert.Equal(t, tt.expectedBodyPayload, w.Body.String(), "Testing "+tt.name)
	}
}

func TestHttpForwardRoute(t *testing.T) {
	router := setupRouter()
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/drivers/test001", nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, 200, w.Code)
	assert.Equal(t, "{\n    \"id\": \"test001\",\n    \"zombie\": true\n}", w.Body.String())
}
