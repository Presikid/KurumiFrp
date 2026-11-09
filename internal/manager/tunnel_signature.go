package manager

import (
	"crypto/sha256"
	"fmt"
	"strings"
)

// tunnelSignature follows the actual frp configuration renderer: diagnostic
// options do not affect it, while equal plaintext secrets remain equivalent
// even if encryption produces a different ciphertext. The digest stays local.
func (s *Store) tunnelSignature(t Tunnel) ([32]byte, error) {
	i, ok := s.DB.State.Instance(t.InstanceID)
	if !ok {
		return [32]byte{}, fmt.Errorf("实例不存在")
	}
	copyStore := *s
	copyStore.DB = s.DB
	copyStore.DB.State = State{Instances: []Instance{i}, Tunnels: []Tunnel{t}}
	r, err := copyStore.render(i.ID, false)
	if err != nil {
		return [32]byte{}, err
	}
	idx := strings.Index(r.Text, "\n[[")
	if idx < 0 {
		return [32]byte{}, fmt.Errorf("规则未启用")
	}
	credential := ""
	if t.Type == "stcp" || t.Type == "stcp-visitor" {
		credential, err = s.decrypt(t.Secret)
		if err != nil {
			return [32]byte{}, err
		}
	}
	return sha256.Sum256([]byte(r.Text[idx:] + "\x00" + credential)), nil
}
