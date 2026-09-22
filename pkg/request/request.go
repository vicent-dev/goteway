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

// @todo refactor to be petition/call and store request + response from gateway call and for internal call
type Request struct {
	HttpRequest  *http.Request
	HttpResponse []byte

	IsInternal bool

	ConvertedRequest string
	Method           string
	Body             io.Reader
}

func NewRequest(r *http.Request, servicesConfig ServicesConfig) (*Request, error) {
	sc, isInternal := findServiceConfigForUri(r.URL.Path, servicesConfig)

	if sc == nil {
		return nil, errors.New("service config not found")
	}

	convertedRequest := strings.Replace(r.URL.Path[1:], sc.Path, sc.Host, -1)

	return &Request{
		r,
		[]byte{},
		isInternal,
		convertedRequest,
		r.Method,
		r.Body,
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

func (r Request) HashKey() string {
	var buf bytes.Buffer
	encoder := base64.NewEncoder(base64.StdEncoding, &buf)
	defer encoder.Close()

	var body []byte
	r.HttpRequest.Body.Read(body)

	encodeKey := struct {
		Url    any
		Header any
		Body   any
		Method any
	}{
		r.HttpRequest.URL,
		r.HttpRequest.Header,
		body,
		r.HttpRequest.Method,
	}

	err := json.NewEncoder(encoder).Encode(fmt.Sprintf("%v", encodeKey))

	if err != nil {
		alog.Error(err.Error(), r)
		return ""
	}

	return buf.String()
}

func (r *Request) Base64Value() string {
	return base64.StdEncoding.EncodeToString(r.HttpResponse)
}

func (r *Request) SetValueFromBase64(s string) {
	data, err := base64.StdEncoding.DecodeString(s)

	if err != nil {
		alog.Error(err.Error())
	}

	r.HttpResponse = data
}

func (r *Request) Value() string {
	return string(r.HttpResponse)
}
