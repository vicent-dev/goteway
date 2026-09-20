package request

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"net/http"

	"github.com/en-vee/alog"
)

type Request struct {
	r        *http.Request
	response string
}

func NewRequest(r *http.Request) *Request {
	return &Request{r, ""}
}

func (r Request) HashKey() string {
	var buf bytes.Buffer
	encoder := base64.NewEncoder(base64.StdEncoding, &buf)
	defer encoder.Close()
	err := json.NewEncoder(encoder).Encode(r)

	if err != nil {
		alog.Error(err.Error(), r)
		return ""
	}

	return buf.String()
}

func (r *Request) Base64Value() string {
	return base64.StdEncoding.EncodeToString([]byte(r.response))
}

func (r *Request) SetValueFromBase64(s string) {
	data, err := base64.StdEncoding.DecodeString(s)

	if err != nil {
		alog.Error(err.Error())
	}

	r.response = string(data)
}

func (r *Request) Value() string {
	return r.response
}
