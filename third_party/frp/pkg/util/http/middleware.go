// Modified by KurumiFRP contributors: route and protect instance logs.

// Copyright 2025 The frp Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package http

import (
	"net/http"

	"github.com/fatedier/frp/pkg/util/xlog"
)

type responseWriter struct {
	http.ResponseWriter
	code int
}

func (rw *responseWriter) WriteHeader(code int) {
	rw.code = code
	rw.ResponseWriter.WriteHeader(code)
}

// WithLogger attaches the owning service logger to HTTP request contexts.
func WithLogger(xl *xlog.Logger) func(http.Handler) http.Handler {
	if xl == nil {
		xl = xlog.New()
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r.WithContext(xlog.NewContext(r.Context(), xl)))
		})
	}
}

func NewRequestLogger(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		xl := xlog.FromContextSafe(r.Context())
		// Paths and query strings can contain credentials supplied by clients.
		xl.Infof("http request: %s", r.Method)
		rw := &responseWriter{ResponseWriter: w, code: http.StatusOK}
		next.ServeHTTP(rw, r)
		xl.Infof("http response: %s code [%d]", r.Method, rw.code)
	})
}
