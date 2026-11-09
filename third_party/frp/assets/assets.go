// Copyright 2016 fatedier, fatedier@gmail.com
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

// Modified by KurumiFRP: isolate web assets per server.

package assets

import (
	"io/fs"
	"net/http"
	"sync"
)

var (
	// content is the embedded read-only filesystem registered at startup.
	content   fs.FS
	contentMu sync.RWMutex
)

type emptyFS struct{}

func (emptyFS) Open(name string) (http.File, error) {
	return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrNotExist}
}

// ForPath returns the assets owned by one web server.
func ForPath(path string) http.FileSystem {
	if path != "" {
		return http.Dir(path)
	}
	contentMu.RLock()
	defer contentMu.RUnlock()
	if content != nil {
		return http.FS(content)
	}
	return emptyFS{}
}

func Register(fileSystem fs.FS) {
	subFs, err := fs.Sub(fileSystem, "dist")
	if err == nil {
		contentMu.Lock()
		content = subFs
		contentMu.Unlock()
	}
}
