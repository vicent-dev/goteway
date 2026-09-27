package app

import (
	"goteway/pkg/log"
	"goteway/pkg/util"
	"net/http"
	"strconv"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

var (
	httpRequestTotal      *prometheus.CounterVec
	httpRequestErrorTotal *prometheus.CounterVec
)

func (s *server) prometheus() {
	httpRequestTotal := prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "api_http_request_total",
		Help: "Total number of requests processed by the API",
	}, []string{"path", "status"})

	httpRequestErrorTotal := prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "api_http_request_error_total",
		Help: "Total number of errors returned by the API",
	}, []string{"path", "status"})

	s.pr = prometheus.NewRegistry()
	s.pr.MustRegister(httpRequestTotal, httpRequestErrorTotal)
}

func (s *server) metricsHandler() func(http.ResponseWriter, *http.Request) {
	h := promhttp.HandlerFor(s.pr, promhttp.HandlerOpts{})
	util.PrintVars("llega")
	return func(w http.ResponseWriter, r *http.Request) {
		util.PrintVars(w, r)
		h.ServeHTTP(w, r)
	}
}

func (s *server) requestMetricsMiddleware(next http.Handler) http.Handler {

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		next.ServeHTTP(w, r)
		status, err := strconv.Atoi(w.Header().Get("StatusCode"))

		status = 200
		if err != nil {
			util.PrintVars(w.Header())
			log.LogWarn(r.Context(), "can't get status from response in middleware")
			return
		}

		if status < 400 {
			httpRequestTotal.WithLabelValues(path, strconv.Itoa(status)).Inc()
		} else {
			httpRequestErrorTotal.WithLabelValues(path, strconv.Itoa(status)).Inc()
		}
	})
}
