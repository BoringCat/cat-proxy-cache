package config

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"fmt"
	"os"

	"github.com/pkg/errors"
)

var (
	ErrDataOrFileMustSet = errors.New("证书数据或文件路径必须设置")
)

type ClientAuthType tls.ClientAuthType

var clientAuthTypeMap = NewMarshalMap(map[string]ClientAuthType{
	"NoClientCert":               ClientAuthType(tls.NoClientCert),
	"RequestClientCert":          ClientAuthType(tls.RequestClientCert),
	"RequireAnyClientCert":       ClientAuthType(tls.RequireAnyClientCert),
	"VerifyClientCertIfGiven":    ClientAuthType(tls.VerifyClientCertIfGiven),
	"RequireAndVerifyClientCert": ClientAuthType(tls.RequireAndVerifyClientCert),
})

func (c *ClientAuthType) UnmarshalText(data []byte) error {
	key := string(data)
	if val, ok := clientAuthTypeMap.Unmarshal(key); ok {
		*c = val
	} else {
		return fmt.Errorf("未知的 ClientAuthType: %s", key)
	}
	return nil
}

func (c ClientAuthType) MarshalText() ([]byte, error) {
	key, ok := clientAuthTypeMap.Marshal(c)
	if !ok {
		return nil, fmt.Errorf("未知的 ClientAuthType: %v", c)
	}
	return []byte(key), nil
}

type Certificate struct {
	Pem      *string `yaml:"pem"`
	PemFile  *string `yaml:"pem_file"`
	Cert     *string `yaml:"cert"`
	CertFile *string `yaml:"cert_file"`
	Key      *string `yaml:"key"`
	KeyFile  *string `yaml:"key_file"`
}

func (c *Certificate) ToCert() (cert tls.Certificate, err error) {
	// var pemData []byte
	var certPEMBlock []byte
	var keyPEMBlock []byte
	if c.Cert != nil {
		certPEMBlock = []byte(*c.Cert)
	} else if c.CertFile != nil {
		certPEMBlock, err = os.ReadFile(*c.CertFile)
		if err != nil {
			return
		}
	} else {
		err = ErrDataOrFileMustSet
		return
	}
	if c.Key != nil {
		keyPEMBlock = []byte(*c.Key)
	} else if c.KeyFile != nil {
		keyPEMBlock, err = os.ReadFile(*c.KeyFile)
		if err != nil {
			return
		}
	} else {
		err = ErrDataOrFileMustSet
		return
	}

	cert, err = tls.X509KeyPair(certPEMBlock, keyPEMBlock)
	return
}

func (c *Certificate) AppendPool(pool *x509.CertPool) (ok bool, err error) {
	var pemBlock []byte
	if c.Pem != nil {
		pemBlock = []byte(*c.Cert)
	} else if c.PemFile != nil {
		pemBlock, err = os.ReadFile(*c.PemFile)
		if err != nil {
			return
		}
	} else {
		err = ErrDataOrFileMustSet
		return
	}
	ok = pool.AppendCertsFromPEM(pemBlock)
	return
}

type Base64Bytes []byte

func (c *Base64Bytes) UnmarshalText(data []byte) error {
	dst := make([]byte, base64.RawStdEncoding.DecodedLen(len(data)))
	_, err := base64.RawStdEncoding.Decode(dst, data)
	if err != nil {
		return err
	}
	*c = dst
	return nil
}
func (c Base64Bytes) MarshalText() ([]byte, error) {
	dst := make([]byte, base64.RawStdEncoding.EncodedLen(len(c)))
	base64.RawStdEncoding.Encode(dst, c)
	return dst, nil
}

type TLSVersion int

var tlsVersionMap = NewMarshalMap(map[string]TLSVersion{
	"TLS 1.0": tls.VersionTLS10,
	"TLS 1.1": tls.VersionTLS11,
	"TLS 1.2": tls.VersionTLS12,
	"TLS 1.3": tls.VersionTLS13,
})

func (c *TLSVersion) UnmarshalText(data []byte) error {
	key := string(data)
	if val, ok := tlsVersionMap.Unmarshal(key); ok {
		*c = val
	} else {
		return fmt.Errorf("未知的 ClientAuthType: %s", key)
	}
	return nil
}

func (c TLSVersion) MarshalText() ([]byte, error) {
	key, ok := tlsVersionMap.Marshal(c)
	if !ok {
		return nil, fmt.Errorf("未知的 ClientAuthType: %v", c)
	}
	return []byte(key), nil
}

type TLSConfig struct {
	Certificates                []*Certificate  `yaml:"certificates,omitempty"`
	RootCAs                     []*Certificate  `yaml:"root_cas,omitempty"`
	DefaultCA                   *bool           `yaml:"include_default_ca,omitempty"`
	NextProtos                  []string        `yaml:"next_protos,omitempty"`
	ServerName                  *string         `yaml:"server_name,omitempty"`
	ClientAuth                  *ClientAuthType `yaml:"client_auth,omitempty"`
	ClientCAs                   []*Certificate  `yaml:"client_cas,omitempty"`
	InsecureSkipVerify          *bool           `yaml:"insecure_skip_verify,omitempty"`
	CipherSuites                []CipherSuites  `yaml:"cipher_suites,omitempty"`
	SessionTicketsDisabled      *bool           `yaml:"session_tickets_disabled,omitempty"`
	SessionTicketKeys           []Base64Bytes   `yaml:"session_ticket_keys,omitempty"`
	MinVersion                  *TLSVersion     `yaml:"min_version,omitempty"`
	MaxVersion                  *TLSVersion     `yaml:"max_version,omitempty"`
	DynamicRecordSizingDisabled *bool           `yaml:"dynamic_record_sizing_disabled,omitempty"`
}

func (c *TLSConfig) NewTlsConfig() (resp *tls.Config, err error) {
	conf := tls.Config{}
	if l := len(c.Certificates); l > 0 {
		conf.Certificates = make([]tls.Certificate, l)
		for i, v := range c.Certificates {
			if conf.Certificates[i], err = v.ToCert(); err != nil {
				err = errors.Wrap(err, "添加证书失败")
				return
			}
		}
	}
	if l := len(c.RootCAs); l > 0 {
		if c.DefaultCA == nil || *c.DefaultCA {
			if conf.RootCAs, err = x509.SystemCertPool(); err != nil {
				err = errors.Wrap(err, "添加CA证书失败")
				return
			}
		} else {
			conf.RootCAs = x509.NewCertPool()
		}
		for _, v := range c.RootCAs {
			if _, err = v.AppendPool(conf.RootCAs); err != nil {
				err = errors.Wrap(err, "添加CA证书失败")
				return
			}
		}
	} else if c.DefaultCA != nil && !*c.DefaultCA {
		// 没有设定CA证书，并且不使用系统证书
		conf.RootCAs = x509.NewCertPool()
	}
	if l := len(c.NextProtos); l > 0 {
		conf.NextProtos = c.NextProtos
	}
	if v := c.ServerName; v != nil {
		conf.ServerName = *v
	}
	if v := c.ClientAuth; v != nil {
		conf.ClientAuth = tls.ClientAuthType(*v)
	}
	if l := len(c.ClientCAs); l > 0 {
		conf.ClientCAs = x509.NewCertPool()
		for _, v := range c.ClientCAs {
			if _, err = v.AppendPool(conf.ClientCAs); err != nil {
				err = errors.Wrap(err, "添加客户端CA证书失败")
				return
			}
		}
	}
	if v := c.InsecureSkipVerify; v != nil {
		conf.InsecureSkipVerify = *v
	}
	if l := len(c.CipherSuites); l > 0 {
		conf.CipherSuites = make([]uint16, l)
		for i, v := range c.CipherSuites {
			conf.CipherSuites[i] = uint16(v)
		}
	}
	if v := c.SessionTicketsDisabled; v != nil {
		conf.SessionTicketsDisabled = *v
	}
	if l := len(c.SessionTicketKeys); l > 0 {
		keys := make([][32]byte, l)
		for i, v := range c.SessionTicketKeys {
			keys[i] = *byte32(v)
		}
		conf.SetSessionTicketKeys(keys)
	}
	if v := c.MinVersion; v != nil {
		conf.MinVersion = uint16(*v)
	}
	if v := c.MaxVersion; v != nil {
		conf.MaxVersion = uint16(*v)
	}
	if v := c.DynamicRecordSizingDisabled; v != nil {
		conf.DynamicRecordSizingDisabled = *v
	}
	resp = &conf
	return
}
