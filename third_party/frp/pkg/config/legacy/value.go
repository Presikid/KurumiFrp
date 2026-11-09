// Copyright 2020 The frp Authors
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

// Modified by KurumiFRP: avoid retaining the process environment globally.

package legacy

import (
	"bytes"
	"os"
	"strings"
	"text/template"
)

type Values struct {
	Envs map[string]string // environment vars
}

// GetValues snapshots the environment only when a file-based template needs it.
func GetValues() *Values {
	envs := make(map[string]string)
	for _, env := range os.Environ() {
		pair := strings.SplitN(env, "=", 2)
		if len(pair) == 2 {
			envs[pair[0]] = pair[1]
		}
	}
	return &Values{Envs: envs}
}

func RenderContent(in []byte) (out []byte, err error) {
	tmpl, errRet := template.New("frp").Parse(string(in))
	if errRet != nil {
		err = errRet
		return
	}

	buffer := bytes.NewBufferString("")
	v := GetValues()
	err = tmpl.Execute(buffer, v)
	if err != nil {
		return
	}
	out = buffer.Bytes()
	return
}

func GetRenderedConfFromFile(path string) (out []byte, err error) {
	var b []byte
	b, err = os.ReadFile(path)
	if err != nil {
		return
	}

	out, err = RenderContent(b)
	return
}
