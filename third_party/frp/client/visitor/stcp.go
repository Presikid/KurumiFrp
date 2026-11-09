// Copyright 2017 fatedier, fatedier@gmail.com
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
// Modified by KurumiFRP to integrate client traffic accounting.
// Modified by KurumiFRP: track restricted STCP visitor workers.

package visitor

import (
	"context"
	"net"
	"strconv"

	libio "github.com/fatedier/golib/io"

	"github.com/fatedier/frp/client/traffic"
	v1 "github.com/fatedier/frp/pkg/config/v1"
	"github.com/fatedier/frp/pkg/util/lifetime"
	"github.com/fatedier/frp/pkg/util/xlog"
)

type STCPVisitor struct {
	*BaseVisitor

	cfg *v1.STCPVisitorConfig
}

func (sv *STCPVisitor) Run() (err error) {
	if sv.cfg.BindPort > 0 {
		sv.l, err = net.Listen("tcp", net.JoinHostPort(sv.cfg.BindAddr, strconv.Itoa(sv.cfg.BindPort)))
		if err != nil {
			return
		}
		if !lifetime.Go(sv.ctx, func() { sv.acceptLoop(sv.l, "stcp local", sv.handleConn) }) {
			sv.Close()
			return context.Canceled
		}
	}

	if !lifetime.Go(sv.ctx, func() { sv.acceptLoop(sv.internalLn, "stcp internal", sv.handleConn) }) {
		sv.Close()
		return context.Canceled
	}

	if sv.plugin != nil {
		sv.plugin.Start()
	}
	return
}

func (sv *STCPVisitor) Close() {
	sv.BaseVisitor.Close()
}

func (sv *STCPVisitor) handleConn(userConn net.Conn) {
	xl := xlog.FromContextSafe(sv.ctx)
	var tunnelErr error
	defer func() {
		if tunnelErr != nil {
			if eConn, ok := userConn.(interface{ CloseWithError(error) error }); ok {
				_ = eConn.CloseWithError(tunnelErr)
				return
			}
		}
		userConn.Close()
	}()

	xl.Debugf("get a new stcp user connection")
	visitorConn, err := sv.dialRawVisitorConn(sv.cfg.GetBaseConfig())
	if err != nil {
		xl.Warnf("dialRawVisitorConn error: %v", err)
		tunnelErr = err
		return
	}
	defer visitorConn.Close()

	remote, recycleFn, err := wrapVisitorConn(visitorConn, sv.cfg.GetBaseConfig())
	if err != nil {
		xl.Warnf("wrapVisitorConn error: %v", err)
		tunnelErr = err
		return
	}
	defer recycleFn()
	if counter := counterForConn(userConn, sv.trafficCounter); counter != nil {
		remote = traffic.WrapStream(remote, counter)
	}

	libio.Join(userConn, remote)
}
