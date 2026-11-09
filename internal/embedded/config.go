// Portions of the template and configuration loading behavior are adapted
// from frp, Copyright 2023-2024 The frp Authors, under the Apache License 2.0.
// See LICENSE-frp in this directory.

// Package embedded prepares per-instance frp configuration in memory.
package embedded

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"text/template"

	v1 "github.com/fatedier/frp/pkg/config/v1"
	"github.com/fatedier/frp/pkg/config/v1/validation"
	"github.com/fatedier/frp/pkg/policy/security"
	"github.com/fatedier/frp/pkg/util/util"
	toml "github.com/pelletier/go-toml/v2"
)

// ClientConfig contains the completed, enabled rules for one client instance.
// Callers must treat the configuration and its fields as sensitive.
type ClientConfig struct {
	Common   *v1.ClientCommonConfig
	Proxies  []v1.ProxyConfigurer
	Visitors []v1.VisitorConfigurer
}

// ParseClientConfig expands and decodes a TOML configuration, applies frp
// defaults and validates the enabled rules. templateEnv consists of KEY=value
// entries like manager.Rendered.Env; values must already be escaped for their
// position in the TOML template. Only these entries are available as .Envs.
// Missing .Envs.KEY entries and duplicate entries are errors. allowedUnsafe is
// empty by default and corresponds to frp's explicit unsafe-feature opt-in.
// Completing the client does not inherit the process http_proxy default.
//
// The second return value is a non-fatal upstream warning. When the final
// return value is non-nil the configuration must not be used. Includes and the
// on-disk store are rejected rather than silently omitted.
func ParseClientConfig(content []byte, templateEnv []string, allowedUnsafe ...string) (*ClientConfig, error, error) {
	data, env, err := renderTOML(content, templateEnv)
	if err != nil {
		return nil, nil, err
	}
	parsed, err := v1.DecodeClientConfigJSON(data, v1.DecodeOptions{DisallowUnknownFields: true})
	if err != nil {
		return nil, nil, diagnostic("decode client configuration", err, env)
	}
	if len(parsed.IncludeConfigFiles) != 0 || parsed.Store.IsEnabled() {
		return nil, nil, errors.New("embedded client configuration does not support includes or an on-disk store")
	}
	if err := parsed.ClientCommonConfig.CompleteWithoutProxyEnvironment(); err != nil {
		return nil, nil, diagnostic("complete client configuration", err, env)
	}

	result := &ClientConfig{Common: &parsed.ClientCommonConfig}
	proxyNames := make(map[string]struct{}, len(parsed.Proxies))
	visitorNames := make(map[string]struct{}, len(parsed.Visitors))
	start := make(map[string]struct{}, len(parsed.Start))
	for _, name := range parsed.Start {
		start[name] = struct{}{}
	}
	for _, typed := range parsed.Proxies {
		proxy := typed.ProxyConfigurer
		base := proxy.GetBaseConfig()
		if base.Name == "" {
			return nil, nil, errors.New("proxy name cannot be empty")
		}
		if _, exists := proxyNames[base.Name]; exists {
			return nil, nil, diagnostic("decode client configuration", fmt.Errorf("proxy name %q is duplicated", base.Name), env)
		}
		proxyNames[base.Name] = struct{}{}
		_, selected := start[base.Name]
		if (len(start) != 0 && !selected) || (base.Enabled != nil && !*base.Enabled) {
			continue
		}
		proxy.Complete()
		result.Proxies = append(result.Proxies, proxy)
	}
	for _, typed := range parsed.Visitors {
		visitor := typed.VisitorConfigurer
		base := visitor.GetBaseConfig()
		if base.Name == "" {
			return nil, nil, errors.New("visitor name cannot be empty")
		}
		if _, exists := visitorNames[base.Name]; exists {
			return nil, nil, diagnostic("decode client configuration", fmt.Errorf("visitor name %q is duplicated", base.Name), env)
		}
		visitorNames[base.Name] = struct{}{}
		_, selected := start[base.Name]
		if (len(start) != 0 && !selected) || (base.Enabled != nil && !*base.Enabled) {
			continue
		}
		visitor.Complete()
		result.Visitors = append(result.Visitors, visitor)
	}

	warning, err := validation.ValidateAllClientConfig(result.Common, result.Proxies, result.Visitors, security.NewUnsafeFeatures(allowedUnsafe))
	if err != nil {
		return nil, diagnostic("client configuration warning", warning, env), diagnostic("validate client configuration", err, env)
	}
	return result, diagnostic("client configuration warning", warning, env), nil
}

// ParseServerConfig performs the same in-memory expansion, upstream defaults
// and validation for a server. Its warning and allowedUnsafe semantics match
// ParseClientConfig.
func ParseServerConfig(content []byte, templateEnv []string, allowedUnsafe ...string) (*v1.ServerConfig, error, error) {
	data, env, err := renderTOML(content, templateEnv)
	if err != nil {
		return nil, nil, err
	}
	var result v1.ServerConfig
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&result); err != nil {
		return nil, nil, diagnostic("decode server configuration", err, env)
	}
	if err := result.Complete(); err != nil {
		return nil, nil, diagnostic("complete server configuration", err, env)
	}
	warning, err := validation.NewConfigValidator(security.NewUnsafeFeatures(allowedUnsafe)).ValidateServerConfig(&result)
	if err != nil {
		return nil, diagnostic("server configuration warning", warning, env), diagnostic("validate server configuration", err, env)
	}
	return &result, diagnostic("server configuration warning", warning, env), nil
}

// renderTOML uses an instance-local template environment. The values provided
// by the manager are already TOML-escaped and are expanded exactly once.
func renderTOML(content []byte, entries []string) ([]byte, map[string]string, error) {
	env := make(map[string]string, len(entries))
	for i, entry := range entries {
		key, value, ok := strings.Cut(entry, "=")
		if !ok || key == "" {
			return nil, nil, fmt.Errorf("invalid template environment entry at index %d", i)
		}
		if _, exists := env[key]; exists {
			return nil, nil, fmt.Errorf("duplicate template environment entry at index %d", i)
		}
		env[key] = value
	}

	tmpl, err := template.New("frp").Option("missingkey=error").Funcs(template.FuncMap{
		"parseNumberRange":     util.ParseRangeNumbers,
		"parseNumberRangePair": numberRangePairs,
	}).Parse(string(content))
	if err != nil {
		return nil, nil, diagnostic("parse configuration template", err, env)
	}
	var rendered bytes.Buffer
	if err := tmpl.Execute(&rendered, struct{ Envs map[string]string }{Envs: env}); err != nil {
		return nil, nil, diagnostic("expand configuration template", err, env)
	}
	var decoded any
	if err := toml.Unmarshal(rendered.Bytes(), &decoded); err != nil {
		return nil, nil, diagnostic("parse TOML", err, env)
	}
	data, err := json.Marshal(decoded)
	if err != nil {
		return nil, nil, diagnostic("encode configuration", err, env)
	}
	return data, env, nil
}

type numberPair struct{ First, Second int64 }

func numberRangePairs(first, second string) ([]numberPair, error) {
	a, err := util.ParseRangeNumbers(first)
	if err != nil {
		return nil, err
	}
	b, err := util.ParseRangeNumbers(second)
	if err != nil {
		return nil, err
	}
	if len(a) != len(b) {
		return nil, errors.New("number ranges have different lengths")
	}
	pairs := make([]numberPair, len(a))
	for i := range a {
		pairs[i] = numberPair{First: a[i], Second: b[i]}
	}
	return pairs, nil
}

// Upstream errors may contain secrets supplied directly in TOML, in addition
// to template values. Returning only the stage avoids leaking either form.
func diagnostic(stage string, err error, _ map[string]string) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%s: details omitted", stage)
}
