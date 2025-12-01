//
// Licensed to the Apache Software Foundation (ASF) under one
// or more contributor license agreements.  See the NOTICE file
// distributed with this work for additional information
// regarding copyright ownership.  The ASF licenses this file
// to you under the Apache License, Version 2.0 (the
// "License"); you may not use this file except in compliance
// with the License.  You may obtain a copy of the License at
//
//   http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing,
// software distributed under the License is distributed on an
// "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY
// KIND, either express or implied.  See the License for the
// specific language governing permissions and limitations
// under the License.
//

package util

import (
	"strconv"
	"testing"
)

func TestGetenv(t *testing.T) {
	tests := []struct {
		name         string
		key          string
		envValue     string
		defaultValue string
		expected     string
		setEnv       bool
	}{
		{
			name:         "environment variable set returns env value",
			key:          "TEST_VAR",
			envValue:     "actual_value",
			defaultValue: "default_value",
			expected:     "actual_value",
			setEnv:       true,
		},
		{
			name:         "environment variable not set returns fallback",
			key:          "UNSET_VAR",
			envValue:     "",
			defaultValue: "default_value",
			expected:     "default_value",
			setEnv:       false,
		},
		{
			// An empty string env var is valid and different from an unset
			// env var util.Getenv uses os.LookupEnv rather than os.Getenv
			// to cater for this distinction.
			name:         "empty string environment variable returns empty string",
			key:          "EMPTY_VAR",
			envValue:     "",
			defaultValue: "",
			expected:     "",
			setEnv:       true,
		},
		{
			name:         "port number as string",
			key:          "TEST_PORT",
			envValue:     "8080",
			defaultValue: "3000",
			expected:     "8080",
			setEnv:       true,
		},
		{
			name:         "URL value",
			key:          "TEST_URL",
			envValue:     "https://example.com/api",
			defaultValue: "http://localhost",
			expected:     "https://example.com/api",
			setEnv:       true,
		},
		{
			name:         "multiline string value",
			key:          "TEST_MULTILINE",
			envValue:     "line1\nline2\nline3",
			defaultValue: "single_line",
			expected:     "line1\nline2\nline3",
			setEnv:       true,
		},
		{
			name:         "special characters in value",
			key:          "TEST_SPECIAL",
			envValue:     "value!@#$%^&*(){}[]|;:',.<>?/~`",
			defaultValue: "default",
			expected:     "value!@#$%^&*(){}[]|;:',.<>?/~`",
			setEnv:       true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if test.setEnv {
				t.Setenv(test.key, test.envValue)
			}
			result := Getenv(test.key, test.defaultValue)

			if result != test.expected {
				t.Errorf("Expected %s, got %s", test.expected, result)
			}
		})
	}
}

func TestGetenvInt(t *testing.T) {
	tests := []struct {
		name         string
		key          string
		envValue     int
		defaultValue int
		expected     int
		setEnv       bool
	}{
		{
			name:         "environment variable set returns env value",
			key:          "TEST_VAR",
			envValue:     10,
			defaultValue: 5,
			expected:     10,
			setEnv:       true,
		},
		{
			name:         "environment variable not set returns fallback",
			key:          "UNSET_VAR",
			envValue:     0,
			defaultValue: 5,
			expected:     5,
			setEnv:       false,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if test.setEnv {
				t.Setenv(test.key, strconv.Itoa(test.envValue))
			}
			result := GetenvInt(test.key, test.defaultValue)

			if result != test.expected {
				t.Errorf("Expected %d, got %d", test.expected, result)
			}
		})
	}
}

func TestGetenvIntFromStrings(t *testing.T) {
	tests := []struct {
		name         string
		key          string
		envValue     string
		defaultValue int
		expected     int
		setEnv       bool
	}{
		{
			name:         "environment variable set int string returns int env value",
			key:          "TEST_VAR",
			envValue:     "10",
			defaultValue: 5,
			expected:     10,
			setEnv:       true,
		},
		{
			name:         "environment variable set float string returns int env value",
			key:          "TEST_VAR",
			envValue:     "10.5",
			defaultValue: 5,
			expected:     10,
			setEnv:       true,
		},
		{
			name:         "environment variable set invalid string returns fallback",
			key:          "UNSET_VAR",
			envValue:     "invalid number",
			defaultValue: 5,
			expected:     5,
			setEnv:       false,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if test.setEnv {
				t.Setenv(test.key, test.envValue)
			}
			result := GetenvInt(test.key, test.defaultValue)

			if result != test.expected {
				t.Errorf("Expected %d, got %d", test.expected, result)
			}
		})
	}
}

func TestInjectCredentialsIntoURINoAuth(t *testing.T) {
	rawURI := "amqp://localhost:5672"
	username := "user"
	password := "pass"

	result := InjectAMQPCredentials(rawURI, username, password)
	expectedPrefix := "amqp://user:pass@localhost:5672"

	if result[:len(expectedPrefix)] != expectedPrefix {
		t.Errorf("Expected prefix %s, got %s", expectedPrefix, result)
	}
}

func TestKeepExistingCredentials(t *testing.T) {
	rawURI := "amqp://existing:auth@localhost:5672"
	username := "newuser"
	password := "newpass"

	result := InjectAMQPCredentials(rawURI, username, password)

	if result != rawURI {
		t.Errorf("Expected URI to remain unchanged, got %s", result)
	}
}

func TestMalformedURI(t *testing.T) {
	rawURI := "://bad_uri"
	username := "user"
	password := "pass"

	result := InjectAMQPCredentials(rawURI, username, password)

	if result != rawURI {
		t.Errorf("Expected malformed URI to return as-is, got %s", result)
	}
}

func TestMissingPassword(t *testing.T) {
	rawURI := "amqp://localhost:5672"
	username := "user"
	password := ""

	result := InjectAMQPCredentials(rawURI, username, password)

	if result != rawURI {
		t.Errorf("Expected URI unchanged when password is missing, got %s", result)
	}
}

func TestMissingUsername(t *testing.T) {
	rawURI := "amqp://localhost:5672"
	username := ""
	password := "pass"

	result := InjectAMQPCredentials(rawURI, username, password)

	if result != rawURI {
		t.Errorf("Expected URI unchanged when username is missing, got %s", result)
	}
}

func TestNoCredentialsProvided(t *testing.T) {
	rawURI := "amqp://localhost:5672"
	username := ""
	password := ""

	result := InjectAMQPCredentials(rawURI, username, password)

	if result != rawURI {
		t.Errorf("Expected URI unchanged when no credentials provided, got %s", result)
	}
}
