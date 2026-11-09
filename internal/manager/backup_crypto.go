package manager

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"unicode/utf8"
)

const (
	backupFormat        = "kurumifrp-encrypted-backup"
	backupVersion       = 1
	backupKDF           = "pbkdf2-hmac-sha256"
	backupIterations    = 600000
	minBackupIterations = 100000
	maxBackupIterations = 1000000
	backupSaltSize      = 16
	backupNonceSize     = 12
	maxBackupSize       = (maxStateSize * 4 / 3) + (2 << 20)
)

type EncryptedBackup struct {
	Format     string `json:"format"`
	Version    int    `json:"version"`
	KDF        string `json:"kdf"`
	Iterations int    `json:"iterations"`
	Salt       string `json:"salt"`
	Nonce      string `json:"nonce"`
	Ciphertext string `json:"ciphertext"`
}

func validateBackupPassword(password string) error {
	if !utf8.ValidString(password) || len(password) < 12 || len(password) > 1024 {
		return fmt.Errorf("backup password must be at least 12 bytes and no more than 1024 bytes")
	}
	for _, c := range []byte(password) {
		if c < 32 || c == 127 {
			return fmt.Errorf("backup password cannot contain control characters")
		}
	}
	return nil
}

func pbkdf2SHA256(password, salt []byte, iterations, keyLen int) ([]byte, error) {
	if iterations < 1 || keyLen < 1 || keyLen > 1024 {
		return nil, fmt.Errorf("invalid PBKDF2 parameters")
	}
	const hLen = sha256.Size
	blocks := (keyLen + hLen - 1) / hLen
	out := make([]byte, 0, blocks*hLen)
	var counter [4]byte
	for block := 1; block <= blocks; block++ {
		binary.BigEndian.PutUint32(counter[:], uint32(block))
		mac := hmac.New(sha256.New, password)
		_, _ = mac.Write(salt)
		_, _ = mac.Write(counter[:])
		u := mac.Sum(nil)
		t := append([]byte(nil), u...)
		for round := 1; round < iterations; round++ {
			mac.Reset()
			_, _ = mac.Write(u)
			u = mac.Sum(u[:0])
			for i := range t {
				t[i] ^= u[i]
			}
		}
		out = append(out, t...)
	}
	return out[:keyLen], nil
}

func encryptBackup(db Database, dataKey []byte, password string) (EncryptedBackup, error) {
	var out EncryptedBackup
	if err := validateBackupPassword(password); err != nil {
		return out, err
	}
	if len(dataKey) != 32 {
		return out, fmt.Errorf("invalid configuration encryption key")
	}
	db.Format = currentDatabaseFormat
	db.Key = base64.RawStdEncoding.EncodeToString(dataKey)
	plain, err := json.Marshal(db)
	if err != nil {
		return out, err
	}
	if len(plain) > maxStateSize {
		return out, fmt.Errorf("backup contents exceed the 128 MiB limit")
	}
	salt := make([]byte, backupSaltSize)
	nonce := make([]byte, backupNonceSize)
	if _, err = rand.Read(salt); err != nil {
		return out, err
	}
	if _, err = rand.Read(nonce); err != nil {
		return out, err
	}
	key, err := pbkdf2SHA256([]byte(password), salt, backupIterations, 32)
	if err != nil {
		return out, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return out, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return out, err
	}
	sealed := gcm.Seal(nil, nonce, plain, []byte("KurumiFRP encrypted backup v1"))
	return EncryptedBackup{
		Format: backupFormat, Version: backupVersion, KDF: backupKDF,
		Iterations: backupIterations,
		Salt:       base64.RawStdEncoding.EncodeToString(salt),
		Nonce:      base64.RawStdEncoding.EncodeToString(nonce),
		Ciphertext: base64.RawStdEncoding.EncodeToString(sealed),
	}, nil
}

func decryptBackup(backup EncryptedBackup, password string) (Database, error) {
	var db Database
	if err := validateBackupPassword(password); err != nil {
		return db, err
	}
	if backup.Format != backupFormat || backup.Version != backupVersion || backup.KDF != backupKDF || backup.Iterations < minBackupIterations || backup.Iterations > maxBackupIterations {
		return db, fmt.Errorf("unsupported encrypted backup format or parameters")
	}
	salt, err := base64.RawStdEncoding.DecodeString(backup.Salt)
	if err != nil || len(salt) != backupSaltSize {
		return db, fmt.Errorf("invalid encrypted backup")
	}
	nonce, err := base64.RawStdEncoding.DecodeString(backup.Nonce)
	if err != nil || len(nonce) != backupNonceSize {
		return db, fmt.Errorf("invalid encrypted backup")
	}
	sealed, err := base64.RawStdEncoding.DecodeString(backup.Ciphertext)
	if err != nil || len(sealed) < 16 || len(sealed) > maxStateSize+32 {
		return db, fmt.Errorf("invalid encrypted backup")
	}
	key, err := pbkdf2SHA256([]byte(password), salt, backup.Iterations, 32)
	if err != nil {
		return db, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return db, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return db, err
	}
	plain, err := gcm.Open(nil, nonce, sealed, []byte("KurumiFRP encrypted backup v1"))
	if err != nil {
		return db, fmt.Errorf("backup password is incorrect or the file is damaged")
	}
	if len(plain) > maxStateSize {
		return db, fmt.Errorf("backup contents exceed the 128 MiB limit")
	}
	db, err = singleFileDatabase(plain)
	if err != nil {
		return db, fmt.Errorf("invalid or unsupported backup contents: %w", err)
	}
	return db, nil
}
