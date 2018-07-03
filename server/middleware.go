// Copyright 2017 HootSuite Media Inc.
//
// Licensed under the Apache License, Version 2.0 (the License);
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//    http://www.apache.org/licenses/LICENSE-2.0
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an AS IS BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
// Modified hereafter by contributors to runatlantis/atlantis.
//
package server

import (
	"fmt"
	"net/http"
	"runtime"
	"strings"

	"github.com/urfave/negroni"
	log "gopkg.in/inconshreveable/log15.v2"
)

// NewRequestLogger creates a RequestLogger.
func NewRequestLogger(logger log.Logger) *RequestLogger {
	return &RequestLogger{logger}
}

// RequestLogger logs requests and their response codes.
type RequestLogger struct {
	logger log.Logger
}

// ServeHTTP implements the middleware function. It logs a request at INFO
// level unless it's a request to /static/*.
func (l *RequestLogger) ServeHTTP(rw http.ResponseWriter, r *http.Request, next http.HandlerFunc) {
	l.logger.Info(fmt.Sprintf("Handling %s %s", r.Method, r.URL.RequestURI()))
	next(rw, r)
	res := rw.(negroni.ResponseWriter)
	if !strings.HasPrefix(r.URL.RequestURI(), "/static") {
		l.logger.Info(fmt.Sprintf("Responded to %s %s", r.Method, r.URL.RequestURI()), "code", res.Status())
	}
}

// Recovery is a Negroni middleware that recovers from any panics and writes a 500 if there was one.
type Recovery struct {
	Logger     log.Logger
	PrintStack bool
	StackAll   bool
	StackSize  int
}

func (rec *Recovery) ServeHTTP(rw http.ResponseWriter, r *http.Request, next http.HandlerFunc) {
	defer func() {
		if err := recover(); err != nil {
			rw.WriteHeader(http.StatusInternalServerError)
			stack := make([]byte, rec.StackSize)
			stack = stack[:runtime.Stack(stack, rec.StackAll)]
			rec.Logger.Error(fmt.Sprintf("PANIC: %s", err), "stack", string(stack))
		}
	}()

	next(rw, r)
}
