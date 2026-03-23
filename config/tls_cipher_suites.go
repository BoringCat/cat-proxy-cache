package config

import "fmt"

type CipherSuites uint16

// A list of cipher suite IDs that are, or have been, implemented by this
// package.
//
// See https://www.iana.org/assignments/tls-parameters/tls-parameters.xml
// See https://pkg.go.dev/crypto/tls#pkg-constants
const (
	// TLS 1.0 - 1.2 cipher suites.
	TLS_RSA_WITH_RC4_128_SHA                      CipherSuites = 0x0005
	TLS_RSA_WITH_3DES_EDE_CBC_SHA                 CipherSuites = 0x000a
	TLS_RSA_WITH_AES_128_CBC_SHA                  CipherSuites = 0x002f
	TLS_RSA_WITH_AES_256_CBC_SHA                  CipherSuites = 0x0035
	TLS_RSA_WITH_AES_128_CBC_SHA256               CipherSuites = 0x003c
	TLS_RSA_WITH_AES_128_GCM_SHA256               CipherSuites = 0x009c
	TLS_RSA_WITH_AES_256_GCM_SHA384               CipherSuites = 0x009d
	TLS_ECDHE_ECDSA_WITH_RC4_128_SHA              CipherSuites = 0xc007
	TLS_ECDHE_ECDSA_WITH_AES_128_CBC_SHA          CipherSuites = 0xc009
	TLS_ECDHE_ECDSA_WITH_AES_256_CBC_SHA          CipherSuites = 0xc00a
	TLS_ECDHE_RSA_WITH_RC4_128_SHA                CipherSuites = 0xc011
	TLS_ECDHE_RSA_WITH_3DES_EDE_CBC_SHA           CipherSuites = 0xc012
	TLS_ECDHE_RSA_WITH_AES_128_CBC_SHA            CipherSuites = 0xc013
	TLS_ECDHE_RSA_WITH_AES_256_CBC_SHA            CipherSuites = 0xc014
	TLS_ECDHE_ECDSA_WITH_AES_128_CBC_SHA256       CipherSuites = 0xc023
	TLS_ECDHE_RSA_WITH_AES_128_CBC_SHA256         CipherSuites = 0xc027
	TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256         CipherSuites = 0xc02f
	TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256       CipherSuites = 0xc02b
	TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384         CipherSuites = 0xc030
	TLS_ECDHE_ECDSA_WITH_AES_256_GCM_SHA384       CipherSuites = 0xc02c
	TLS_ECDHE_RSA_WITH_CHACHA20_POLY1305_SHA256   CipherSuites = 0xcca8
	TLS_ECDHE_ECDSA_WITH_CHACHA20_POLY1305_SHA256 CipherSuites = 0xcca9

	// TLS 1.3 cipher suites.
	TLS_AES_128_GCM_SHA256       CipherSuites = 0x1301
	TLS_AES_256_GCM_SHA384       CipherSuites = 0x1302
	TLS_CHACHA20_POLY1305_SHA256 CipherSuites = 0x1303

	// TLS_FALLBACK_SCSV isn't a standard cipher suite but an indicator
	// that the client is doing version fallback. See RFC 7507.
	TLS_FALLBACK_SCSV CipherSuites = 0x5600

	// Legacy names for the corresponding cipher suites with the correct _SHA256
	// suffix, retained for backward compatibility.
	TLS_ECDHE_RSA_WITH_CHACHA20_POLY1305   = TLS_ECDHE_RSA_WITH_CHACHA20_POLY1305_SHA256
	TLS_ECDHE_ECDSA_WITH_CHACHA20_POLY1305 = TLS_ECDHE_ECDSA_WITH_CHACHA20_POLY1305_SHA256
)

var cipherSuitesMap = NewMarshalMap(map[string]CipherSuites{
	"TLS_RSA_WITH_RC4_128_SHA":                      TLS_RSA_WITH_RC4_128_SHA,
	"TLS_RSA_WITH_3DES_EDE_CBC_SHA":                 TLS_RSA_WITH_3DES_EDE_CBC_SHA,
	"TLS_RSA_WITH_AES_128_CBC_SHA":                  TLS_RSA_WITH_AES_128_CBC_SHA,
	"TLS_RSA_WITH_AES_256_CBC_SHA":                  TLS_RSA_WITH_AES_256_CBC_SHA,
	"TLS_RSA_WITH_AES_128_CBC_SHA256":               TLS_RSA_WITH_AES_128_CBC_SHA256,
	"TLS_RSA_WITH_AES_128_GCM_SHA256":               TLS_RSA_WITH_AES_128_GCM_SHA256,
	"TLS_RSA_WITH_AES_256_GCM_SHA384":               TLS_RSA_WITH_AES_256_GCM_SHA384,
	"TLS_ECDHE_ECDSA_WITH_RC4_128_SHA":              TLS_ECDHE_ECDSA_WITH_RC4_128_SHA,
	"TLS_ECDHE_ECDSA_WITH_AES_128_CBC_SHA":          TLS_ECDHE_ECDSA_WITH_AES_128_CBC_SHA,
	"TLS_ECDHE_ECDSA_WITH_AES_256_CBC_SHA":          TLS_ECDHE_ECDSA_WITH_AES_256_CBC_SHA,
	"TLS_ECDHE_RSA_WITH_RC4_128_SHA":                TLS_ECDHE_RSA_WITH_RC4_128_SHA,
	"TLS_ECDHE_RSA_WITH_3DES_EDE_CBC_SHA":           TLS_ECDHE_RSA_WITH_3DES_EDE_CBC_SHA,
	"TLS_ECDHE_RSA_WITH_AES_128_CBC_SHA":            TLS_ECDHE_RSA_WITH_AES_128_CBC_SHA,
	"TLS_ECDHE_RSA_WITH_AES_256_CBC_SHA":            TLS_ECDHE_RSA_WITH_AES_256_CBC_SHA,
	"TLS_ECDHE_ECDSA_WITH_AES_128_CBC_SHA256":       TLS_ECDHE_ECDSA_WITH_AES_128_CBC_SHA256,
	"TLS_ECDHE_RSA_WITH_AES_128_CBC_SHA256":         TLS_ECDHE_RSA_WITH_AES_128_CBC_SHA256,
	"TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256":         TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256,
	"TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256":       TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256,
	"TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384":         TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384,
	"TLS_ECDHE_ECDSA_WITH_AES_256_GCM_SHA384":       TLS_ECDHE_ECDSA_WITH_AES_256_GCM_SHA384,
	"TLS_ECDHE_RSA_WITH_CHACHA20_POLY1305_SHA256":   TLS_ECDHE_RSA_WITH_CHACHA20_POLY1305_SHA256,
	"TLS_ECDHE_ECDSA_WITH_CHACHA20_POLY1305_SHA256": TLS_ECDHE_ECDSA_WITH_CHACHA20_POLY1305_SHA256,
	"TLS_AES_128_GCM_SHA256":                        TLS_AES_128_GCM_SHA256,
	"TLS_AES_256_GCM_SHA384":                        TLS_AES_256_GCM_SHA384,
	"TLS_CHACHA20_POLY1305_SHA256":                  TLS_CHACHA20_POLY1305_SHA256,
	"TLS_FALLBACK_SCSV":                             TLS_FALLBACK_SCSV,
	"TLS_ECDHE_RSA_WITH_CHACHA20_POLY1305":          TLS_ECDHE_RSA_WITH_CHACHA20_POLY1305,
	"TLS_ECDHE_ECDSA_WITH_CHACHA20_POLY1305":        TLS_ECDHE_ECDSA_WITH_CHACHA20_POLY1305,
})

func (c *CipherSuites) UnmarshalText(data []byte) error {
	key := string(data)
	if val, ok := cipherSuitesMap.Unmarshal(key); ok {
		*c = val
	} else {
		return fmt.Errorf("未知的 CipherSuites: %s", key)
	}
	return nil
}

func (c CipherSuites) MarshalText() ([]byte, error) {
	key, ok := cipherSuitesMap.Marshal(c)
	if !ok {
		return nil, fmt.Errorf("未知的 CipherSuites: %v", c)
	}
	return []byte(key), nil
}
