package pmax

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strconv"

	"github.com/dell/csi-powermax/pmax/api"
	types "github.com/dell/csi-powermax/pmax/types/v90"
	log "github.com/sirupsen/logrus"
)

// Client is the callers handle to the pmax client library.
// Obtain a client by calling NewClient.
type Client struct {
	configConnect *ConfigConnect
	api           api.Client
	allowedArrays []string
}

var (
	errNilReponse   = errors.New("nil response from API")
	errBodyRead     = errors.New("error reading body")
	errNoLink       = errors.New("Error: problem finding link")
	debug, _        = strconv.ParseBool(os.Getenv("X_CSI_POWERMAX_DEBUG"))
	accHeader       string
	conHeader       string
	applicationType string
)

// Authenticate and get API version
func (c *Client) Authenticate(configConnect *ConfigConnect) error {
	if debug {
		log.Printf("PowerMax debug: %v\n", debug)
		log.SetLevel(log.DebugLevel)
	}

	c.configConnect = configConnect
	c.api.SetToken("")
	basicAuthString := basicAuth(configConnect.Username, configConnect.Password)

	headers := make(map[string]string, 1)
	headers["Authorization"] = "Basic " + basicAuthString

	resp, err := c.api.DoAndGetResponseBody(
		context.Background(), http.MethodGet, "univmax/restapi/system/version", headers, nil)
	if err != nil {
		doLog(log.WithError(err).Error, "")
		return err
	}
	defer resp.Body.Close()

	// parse the response
	switch {
	case resp == nil:
		return errNilReponse
	case !(resp.StatusCode >= 200 && resp.StatusCode <= 299):
		return c.api.ParseJSONError(resp)
	}

	version := &types.Version{}
	decoder := json.NewDecoder(resp.Body)
	err = decoder.Decode(version)
	if err != nil {
		return err
	}
	log.Printf("API version: %s\n", version.Version)

	return nil
}

// Generate the base 64 Authorization string from username / password
func basicAuth(username, password string) string {
	auth := username + ":" + password
	return base64.StdEncoding.EncodeToString([]byte(auth))
}

func doLog(
	l func(args ...interface{}),
	msg string) {

	if debug {
		l(msg)
	}
}

// NewClient returns a new Client, which is of interface type Pmax.
// The Client holds state for the connection.
// Thhe following environment variables define the connection:
//    CSI_POWERMAX_ENDPOINT - A URL of the form https://1.2.3.4:8443
//    CSI_POWERMAX_VERSION - should not be used. Defines a particular form of versioning.
//    CSI_APPLICATION_NAME - Application name which will be used for registering the application with Unisphere REST APIs
//    CSI_POWERMAX_INSECURE - A boolean indicating whether unvalidated certificates can be accepted. Defaults to true.
//    CSI_POWERMAX_USECERTS - Indicates whether to use certificates at all. Defaults to true.
func NewClient() (client Pmax, err error) {
	return NewClientWithArgs(
		os.Getenv("CSI_POWERMAX_ENDPOINT"),
		os.Getenv("CSI_POWERMAX_VERSION"),
		os.Getenv("CSI_APPLICATION_NAME"),
		os.Getenv("CSI_POWERMAX_INSECURE") == "true",
		os.Getenv("CSI_POWERMAX_USECERTS") == "true")
}

// NewClientWithArgs allows the user to specify the endpoint, version, application name, insecure boolean, and useCerts boolean
// as direct arguments rather than receiving them from the enviornment. See NewClient().
func NewClientWithArgs(
	endpoint string,
	version string,
	applicationName string,
	insecure,
	useCerts bool) (client Pmax, err error) {

	fields := map[string]interface{}{
		"endpoint":        endpoint,
		"applicationName": applicationName,
		"insecure":        insecure,
		"useCerts":        useCerts,
		"version":         version,
		"debug":           debug,
	}

	doLog(log.WithFields(fields).Debug, "pmax client init")

	if endpoint == "" {
		doLog(log.WithFields(fields).Error, "endpoint is required")
		return nil, fmt.Errorf("Endpoint must be supplied, e.g. https://1.2.3.4:8443")
	}

	opts := api.ClientOptions{
		Insecure: insecure,
		UseCerts: useCerts,
		ShowHTTP: debug,
	}

	if applicationType != "" {
		log.Debug(fmt.Sprintf("Application type already set to: %s, Resetting it to: %s",
			applicationType, applicationName))
	}
	applicationType = applicationName

	ac, err := api.New(context.Background(), endpoint, opts, debug)
	if err != nil {
		doLog(log.WithError(err).Error, "Unable to create HTTP client")
		return nil, err
	}

	client = &Client{
		api: ac,
		configConnect: &ConfigConnect{
			Version: version,
		},
		allowedArrays: []string{},
	}

	accHeader = api.HeaderValContentTypeJSON
	if version != "" {
		accHeader = accHeader + ";version=" + version
	}
	conHeader = accHeader

	return client, nil
}

func (c *Client) getDefaultHeaders() map[string]string {
	headers := make(map[string]string)
	headers["Accept"] = accHeader
	if applicationType != "" {
		headers["Application-Type"] = applicationType
	}
	headers["Content-Type"] = conHeader
	basicAuthString := basicAuth(c.configConnect.Username, c.configConnect.Password)
	headers["Authorization"] = "Basic " + basicAuthString
	return headers
}
