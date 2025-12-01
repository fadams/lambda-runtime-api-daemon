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
	"net/url"
	"os"
	"strconv"
)

// Getenv retrieves the value of the environment variable named by the key.
// It returns the value, or fallback if the variable is not present.
// This helper provides similar behaviour to Python's os.getenv(). Note we use
// os.LookupEnv not os.Getenv to cater for unset environment variables.
func Getenv(key string, fallback string) string {
	if value, ok := os.LookupEnv(key); ok {
		return value
	}
	return fallback
}

// GetenvInt retrieves the value of the environment variable named by the key.
// It returns the value as an int, or fallback if the variable is not present.
// This helper provides similar behaviour to Python's os.getenv(). Note we use
// os.LookupEnv not os.Getenv to cater for unset environment variables.
func GetenvInt(key string, fallback int) int {
	if stringValue, ok := os.LookupEnv(key); ok {
		if value, err := strconv.ParseFloat(stringValue, 64); err == nil {
			return int(value)
		}
		return fallback
	}
	return fallback
}

func InjectAMQPCredentials(rawURI, username, password string) string {
	parsed, err := url.Parse(rawURI)
	if err != nil {
		// If it's not a valid URL, fallback to raw URI
		return rawURI
	}

	// If the URI already includes user info, return as-is
	if parsed.User != nil {
		return rawURI
	}

	// Only inject if both username and password are provided
	if username != "" && password != "" {
		parsed.User = url.UserPassword(username, password)
		return parsed.String()
	}

	// No user info provided
	return rawURI
}
