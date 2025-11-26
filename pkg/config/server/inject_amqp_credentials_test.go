package server

import "testing"

func TestInjectCredentialsIntoURINoAuth(t *testing.T) {
	rawURI := "amqp://localhost:5672"
	username := "user"
	password := "pass"

	result := injectAMQPCredentials(rawURI, username, password)
	expectedPrefix := "amqp://user:pass@localhost:5672"

	if result[:len(expectedPrefix)] != expectedPrefix {
		t.Errorf("Expected prefix %s, got %s", expectedPrefix, result)
	}
}

func TestKeepExistingCredentials(t *testing.T) {
	rawURI := "amqp://existing:auth@localhost:5672"
	username := "newuser"
	password := "newpass"

	result := injectAMQPCredentials(rawURI, username, password)

	if result != rawURI {
		t.Errorf("Expected URI to remain unchanged, got %s", result)
	}
}

func TestMalformedURI(t *testing.T) {
	rawURI := "://bad_uri"
	username := "user"
	password := "pass"

	result := injectAMQPCredentials(rawURI, username, password)

	if result != rawURI {
		t.Errorf("Expected malformed URI to return as-is, got %s", result)
	}
}

func TestMissingPassword(t *testing.T) {
	rawURI := "amqp://localhost:5672"
	username := "user"
	password := ""

	result := injectAMQPCredentials(rawURI, username, password)

	if result != rawURI {
		t.Errorf("Expected URI unchanged when password is missing, got %s", result)
	}
}

func TestMissingUsername(t *testing.T) {
	rawURI := "amqp://localhost:5672"
	username := ""
	password := "pass"

	result := injectAMQPCredentials(rawURI, username, password)

	if result != rawURI {
		t.Errorf("Expected URI unchanged when username is missing, got %s", result)
	}
}

func TestNoCredentialsProvided(t *testing.T) {
	rawURI := "amqp://localhost:5672"
	username := ""
	password := ""

	result := injectAMQPCredentials(rawURI, username, password)

	if result != rawURI {
		t.Errorf("Expected URI unchanged when no credentials provided, got %s", result)
	}
}
