// Copyright 2020 guylewin, guy@lewin.co.il
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

// Modified by KurumiFRP: use a per-server custom 404 page.

// Modified by KurumiFRP: track per-service multiplexer lifetime.

package tcpmux

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"time"

	libnet "github.com/fatedier/golib/net"

	httppkg "github.com/fatedier/frp/pkg/util/http"
	"github.com/fatedier/frp/pkg/util/vhost"
)

type HTTPConnectTCPMuxer struct {
	*vhost.Muxer

	// If passthrough is set to true, the CONNECT request will be forwarded to the backend service.
	// Otherwise, it will return an OK response to the client and forward the remaining content to the backend service.
	passthrough      bool
	notFoundPagePath string
}

func NewHTTPConnectTCPMuxer(listener net.Listener, passthrough bool, timeout time.Duration) (*HTTPConnectTCPMuxer, error) {
	return NewHTTPConnectTCPMuxerWithNotFoundPage(listener, passthrough, timeout, "")
}

// NewHTTPConnectTCPMuxerWithNotFoundPage sets the 404 page for this muxer.
func NewHTTPConnectTCPMuxerWithNotFoundPage(listener net.Listener, passthrough bool, timeout time.Duration, pagePath string) (*HTTPConnectTCPMuxer, error) {
	return NewHTTPConnectTCPMuxerWithContext(context.Background(), listener, passthrough, timeout, pagePath)
}

func NewHTTPConnectTCPMuxerWithContext(ctx context.Context, listener net.Listener, passthrough bool, timeout time.Duration, pagePath string) (*HTTPConnectTCPMuxer, error) {
	ret := &HTTPConnectTCPMuxer{passthrough: passthrough, notFoundPagePath: pagePath}
	mux, err := vhost.NewMuxerConfiguredWithContext(ctx, listener, ret.getHostFromHTTPConnect, timeout, func(mux *vhost.Muxer) {
		mux.SetCheckAuthFunc(ret.auth).
			SetSuccessHookFunc(ret.sendConnectResponse).
			SetFailHookFunc(ret.vhostFailed)
	})
	if err != nil {
		return nil, err
	}
	ret.Muxer = mux
	return ret, nil
}

func (muxer *HTTPConnectTCPMuxer) readHTTPConnectRequest(rd io.Reader) (host, httpUser, httpPwd string, err error) {
	bufioReader := bufio.NewReader(rd)

	req, err := http.ReadRequest(bufioReader)
	if err != nil {
		return
	}

	if req.Method != "CONNECT" {
		err = fmt.Errorf("connections to tcp vhost must be of method CONNECT")
		return
	}

	host, _ = httppkg.CanonicalHost(req.Host)
	proxyAuth := req.Header.Get("Proxy-Authorization")
	if proxyAuth != "" {
		httpUser, httpPwd, _ = httppkg.ParseBasicAuth(proxyAuth)
	}
	return
}

func (muxer *HTTPConnectTCPMuxer) sendConnectResponse(c net.Conn, _ map[string]string) error {
	if muxer.passthrough {
		return nil
	}
	res := httppkg.OkResponse()
	if res.Body != nil {
		defer res.Body.Close()
	}
	return res.Write(c)
}

func (muxer *HTTPConnectTCPMuxer) auth(c net.Conn, username, password string, reqInfo map[string]string) (bool, error) {
	reqUsername := reqInfo["HTTPUser"]
	reqPassword := reqInfo["HTTPPwd"]
	if username == reqUsername && password == reqPassword {
		return true, nil
	}

	resp := httppkg.ProxyUnauthorizedResponse()
	if resp.Body != nil {
		defer resp.Body.Close()
	}
	_ = resp.Write(c)
	return false, nil
}

func (muxer *HTTPConnectTCPMuxer) vhostFailed(c net.Conn) {
	res := vhost.NotFoundResponseForPage(muxer.notFoundPagePath)
	if res.Body != nil {
		defer res.Body.Close()
	}
	_ = res.Write(c)
	_ = c.Close()
}

func (muxer *HTTPConnectTCPMuxer) getHostFromHTTPConnect(c net.Conn) (net.Conn, map[string]string, error) {
	reqInfoMap := make(map[string]string, 0)
	sc, rd := libnet.NewSharedConn(c)

	host, httpUser, httpPwd, err := muxer.readHTTPConnectRequest(rd)
	if err != nil {
		return nil, reqInfoMap, err
	}

	reqInfoMap["Host"] = host
	reqInfoMap["Scheme"] = "tcp"
	reqInfoMap["HTTPUser"] = httpUser
	reqInfoMap["HTTPPwd"] = httpPwd

	outConn := c
	if muxer.passthrough {
		outConn = sc
	}
	return outConn, reqInfoMap, nil
}
