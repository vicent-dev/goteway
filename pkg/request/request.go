package request

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/en-vee/alog"
)

type Request struct {
	HttpRequest  *http.Request
	HttpResponse []byte
}

func NewRequest(r *http.Request) *Request {
	return &Request{r, []byte{}}
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
