package manager

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"
)

const (
	outboundAgentDefaultInterval = 30 * time.Second
	outboundAgentMinIntervalSeconds = 15
	outboundAgentMaxIntervalSeconds = 60
	outboundAgentMaxPendingAge = 30 * time.Second
	outboundAgentTimeout = 5 * time.Second
	outboundAgentResponseLimit = 16 << 10
	outboundAgentRequestLimit = 64 << 10
)

var (
	errOutboundAgentAuthorization = errors.New("目标拒绝请求，请检查授权和 HTTPS 代理配置")
	errOutboundAgentConflict = errors.New("节点上报会话已失效或序号冲突")
	errOutboundAgentAddressChanged = errors.New("上报目标解析地址已变化，请重新确认")
	errOutboundAgentIdentity = errors.New("上报目标返回的节点身份不匹配")
	errOutboundAgentProtocol = errors.New("上报目标不接受当前协议，请核对地址和版本")
	errOutboundAgentTemporary = errors.New("节点上报暂时不可用")
	errOutboundAgentSessionRenew = errors.New("节点上报会话需要重新建立")
)

// outboundAgentSender belongs to a single worker and is not concurrency-safe.
// The owner cancels its context and waits for that worker before replacing it.
// Pinned addresses must have been explicitly confirmed for this origin.
type outboundAgentSender struct {
	origin string
	addresses []string
	credential string
	expectedNodeID string
	allowTraffic bool

	session string
	nextSequence uint64
	pending []byte
	pendingSince time.Time
	interval time.Duration
}

type outboundAgentSessionResponse struct {
	Protocol int `json:"protocol"`
	NodeID string `json:"nodeId"`
	Session string `json:"session"`
	NextSequence string `json:"nextSequence"`
	IntervalSeconds int `json:"intervalSeconds"`
}

type outboundAgentReportResponse struct {
	Protocol int `json:"protocol"`
	Sequence string `json:"sequence"`
	NextSequence string `json:"nextSequence"`
	IntervalSeconds int `json:"intervalSeconds"`
}

func newOutboundAgentSender(origin string, addresses []string, credential, nodeID string, allowTraffic bool) (*outboundAgentSender, error) {
	normalized, err := normalizeNodeURL(origin)
	if err != nil || !validPinnedAddresses(addresses, false) || !nodeTokenPattern.MatchString(credential) || !idPattern.MatchString(nodeID) {
		return nil, fmt.Errorf("节点上报配置无效或地址尚未确认")
	}
	return &outboundAgentSender{
		origin: normalized,
		addresses: append([]string(nil), addresses...),
		credential: credential,
		expectedNodeID: nodeID,
		allowTraffic: allowTraffic,
		interval: outboundAgentDefaultInterval,
	}, nil
}

func outboundAgentDecimal(value string) (uint64, bool) {
	parsed, err := strconv.ParseUint(value, 10, 64)
	return parsed, err == nil && parsed != 0 && strconv.FormatUint(parsed, 10) == value
}

func outboundAgentInterval(seconds int) (time.Duration, bool) {
	if seconds < outboundAgentMinIntervalSeconds || seconds > outboundAgentMaxIntervalSeconds {
		return 0, false
	}
	return time.Duration(seconds) * time.Second, true
}

// post uses only a fixed protocol path, verifies the entire DNS set before
// attaching the credential, and dials only confirmed addresses. No response
// body or network error is returned to the caller as it may contain secrets.
func (s *outboundAgentSender) post(parent context.Context, path string, body []byte, result any) error {
	if parent == nil || (path != "/api/node-agent/session" && path != "/api/node-agent/report") || len(body) == 0 || len(body) > outboundAgentRequestLimit {
		return fmt.Errorf("节点上报请求无效")
	}
	ctx, cancel := context.WithTimeout(parent, outboundAgentTimeout)
	defer cancel()
	resolved, err := resolveNodeAddresses(ctx, s.origin)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return errOutboundAgentTemporary
	}
	if !sameNodeAddresses(resolved, s.addresses) {
		return errOutboundAgentAddressChanged
	}
	dial, err := pinnedNodeDialer(s.origin, s.addresses)
	if err != nil {
		return errOutboundAgentAddressChanged
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, s.origin+path, bytes.NewReader(body))
	if err != nil {
		return errOutboundAgentTemporary
	}
	request.Header.Set("Authorization", "Bearer "+s.credential)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json")
	transport := &http.Transport{Proxy: nil, DialContext: dial, DisableKeepAlives: true, TLSHandshakeTimeout: 3 * time.Second, ResponseHeaderTimeout: 4 * time.Second, MaxResponseHeaderBytes: 16 << 10}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Do(request)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return errOutboundAgentTemporary
	}
	defer response.Body.Close()
	switch response.StatusCode {
	case http.StatusUnauthorized, http.StatusForbidden:
		return errOutboundAgentAuthorization
	case http.StatusConflict:
		return errOutboundAgentConflict
	case http.StatusOK:
	default:
		if response.StatusCode >= 300 && response.StatusCode < 500 && response.StatusCode != http.StatusRequestTimeout && response.StatusCode != http.StatusTooManyRequests {
			return errOutboundAgentProtocol
		}
		return errOutboundAgentTemporary
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, outboundAgentResponseLimit+1))
	if err != nil || len(data) > outboundAgentResponseLimit || decodeStrictJSON(data, result) != nil {
		return errOutboundAgentTemporary
	}
	return nil
}

// A new session invalidates the previous session even when its reply is lost.
func (s *outboundAgentSender) openSession(ctx context.Context) error {
	s.session, s.nextSequence, s.pending, s.pendingSince = "", 0, nil, time.Time{}
	var reply outboundAgentSessionResponse
	if err := s.post(ctx, "/api/node-agent/session", []byte(`{"protocol":1}`), &reply); err != nil {
		return err
	}
	if reply.NodeID != s.expectedNodeID || !idPattern.MatchString(reply.NodeID) {
		return errOutboundAgentIdentity
	}
	sequence, ok := outboundAgentDecimal(reply.NextSequence)
	interval, validInterval := outboundAgentInterval(reply.IntervalSeconds)
	if reply.Protocol != 1 || !nodeTokenPattern.MatchString(reply.Session) || !ok || sequence != 1 || !validInterval {
		return errOutboundAgentTemporary
	}
	s.session, s.nextSequence, s.interval = reply.Session, sequence, interval
	return nil
}

// prepareReport retains the exact serialized bytes until the receiver confirms
// them. A changed snapshot cannot silently replace an uncertain report.
func (s *outboundAgentSender) prepareReport(summary nodeSummary) error {
	if s.session == "" || s.nextSequence == 0 || s.nextSequence == ^uint64(0) {
		return errOutboundAgentSessionRenew
	}
	if s.pending != nil {
		return fmt.Errorf("上次节点上报尚未确认")
	}
	if !validNodeSummary(summary) {
		return fmt.Errorf("节点摘要暂不可用")
	}
	for _, instance := range summary.Instances {
		if instance.CanControl || (!s.allowTraffic && instance.Traffic != nil) {
			return fmt.Errorf("节点摘要包含未授权的内容")
		}
	}
	body, err := json.Marshal(struct {
		Protocol int `json:"protocol"`
		Session string `json:"session"`
		Sequence string `json:"sequence"`
		Summary nodeSummary `json:"summary"`
	}{1, s.session, strconv.FormatUint(s.nextSequence, 10), summary})
	if err != nil || len(body) > outboundAgentRequestLimit {
		return fmt.Errorf("节点摘要无法发送")
	}
	s.pending = body
	s.pendingSince = time.Now()
	return nil
}

// sendPending leaves an uncertain request intact for a byte-identical retry.
// A 409 invalidates the session; a 401/403 must stop retries until reconfigured.
func (s *outboundAgentSender) sendPending(ctx context.Context) error {
	if s.session == "" || s.nextSequence == 0 || len(s.pending) == 0 {
		return fmt.Errorf("没有待发送的节点上报")
	}
	var reply outboundAgentReportResponse
	err := s.post(ctx, "/api/node-agent/report", s.pending, &reply)
	if errors.Is(err, errOutboundAgentConflict) || errors.Is(err, errOutboundAgentAuthorization) {
		s.session, s.nextSequence, s.pending, s.pendingSince = "", 0, nil, time.Time{}
		return err
	}
	if err != nil {
		return err
	}
	sequence, validSequence := outboundAgentDecimal(reply.Sequence)
	next, validNext := outboundAgentDecimal(reply.NextSequence)
	interval, validInterval := outboundAgentInterval(reply.IntervalSeconds)
	if reply.Protocol != 1 || !validSequence || !validNext || sequence != s.nextSequence || next != sequence+1 || !validInterval {
		return errOutboundAgentTemporary
	}
	s.nextSequence, s.pending, s.pendingSince, s.interval = next, nil, time.Time{}, interval
	return nil
}
