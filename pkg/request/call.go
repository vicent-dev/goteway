package request

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"

	"github.com/en-vee/alog"
)

type serializedResponse struct {
	Status     string         `json:"status"`
	StatusCode int            `json:"status_code"`
	Body       string         `json:"body"`
	Header     http.Header    `json:"header"`
	Cookies    []*http.Cookie `json:"cookies"`
}

type Call struct {
	id         string // key generated from gateway request
	isInternal bool
	requestUrl string
	request    *http.Request
	response   *http.Response
}

func NewCall(r *http.Request, servicesConfig ServicesConfig) (*Call, error) {
	sc, isInternal := findServiceConfigForUri(r.URL.Path, servicesConfig)

	if sc == nil {
		return nil, errors.New("service config not found")
	}

	requestUrl := strings.Replace(r.URL.Path[1:], sc.Path, sc.Host, -1)

	// generate key
	var buf bytes.Buffer
	encoder := base64.NewEncoder(base64.StdEncoding, &buf)
	defer encoder.Close()
	defer r.Body.Close()

	header := r.Header
	// @todo check other time based headers
	header.Del("Date")

	body, _ := io.ReadAll(r.Body)

	encodeKey := struct {
		Url     any
		Header  any
		Body    any
		Method  any
		Cookies any
	}{
		r.URL,
		header,
		body,
		r.Method,
		r.Cookies(),
	}

	err := json.NewEncoder(encoder).Encode(fmt.Sprintf("%v", encodeKey))

	if err != nil {
		return nil, err
	}

	r.Body = io.NopCloser(bytes.NewBuffer(body))
	alog.Info(fmt.Sprintf("[%v] %v -> %v", r.Method, r.URL, string(body)))

	return &Call{
		id:         buf.String(),
		isInternal: isInternal,
		requestUrl: requestUrl,
		request:    r,
		response:   nil,
	}, nil
}

func findServiceConfigForUri(uri string, servicesConfig ServicesConfig) (*ServiceConfig, bool) {

	var sc *ServiceConfig
	isInternal := false

	for _, s := range servicesConfig.External {

		if matched, _ := regexp.MatchString(s.Path+"*", uri); matched {
			sc = &s
			break
		}
	}

	for _, s := range servicesConfig.Internal {

		if matched, _ := regexp.MatchString(s.Path+"*", uri); matched {

			if sc == nil || (sc != nil && strings.Count(s.Path, "/") > strings.Count(sc.Path, "/")) {
				isInternal = true
				sc = &s
				break
			}

		}
	}

	return sc, isInternal
}

func (c *Call) Key() string {
	return c.id
}

func (c *Call) Value() string {

	defer c.response.Body.Close()

	body, _ := io.ReadAll(c.response.Body)

	serialized := serializedResponse{
		c.response.Status,
		c.response.StatusCode,
		string(body),
		c.response.Header,
		c.response.Cookies(),
	}

	v, err := json.Marshal(serialized)

	if err != nil {
		alog.Error(err.Error())
		return ""
	}

	return string(v)
}

func (c *Call) SetValue(s string) {
	serialized := &serializedResponse{}
	err := json.Unmarshal([]byte(s), serialized)

	if err != nil {
		alog.Error(err.Error())
		return
	}

	c.response = &http.Response{}
	c.response.Status = serialized.Status
	c.response.StatusCode = serialized.StatusCode
	c.response.Body = io.NopCloser(bytes.NewReader([]byte(serialized.Body)))
	c.response.Header = serialized.Header
}
