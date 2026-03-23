package config

import (
	"bytes"
	"crypto/tls"
	"testing"

	"github.com/goccy/go-yaml"
)

func TestUnmarshal(t *testing.T) {
	t.Run("ClientAuth: VerifyClientCertIfGiven", func(t *testing.T) {
		want := ClientAuthType(tls.VerifyClientCertIfGiven)
		data := `client_auth: VerifyClientCertIfGiven`
		var config TLSConfig
		if err := yaml.Unmarshal([]byte(data), &config); err != nil {
			t.Fatal(err)
		}
		if config.ClientAuth == nil {
			t.Fatal("数据解析失败。ClientAuth == nil")
		}
		if *config.ClientAuth != want {
			t.Fatalf("数据解析失败。期望: %v, 得到: %v", want, config.ClientAuth)
		}
	})
	t.Run("ClientAuth: UnKnown", func(t *testing.T) {
		data := `client_auth: UnKnown`
		var config TLSConfig
		if err := yaml.Unmarshal([]byte(data), &config); err == nil {
			t.Fatal("数据解析成功。")
		}
	})
	t.Run("CipherSuites: TLS_ECDHE_ECDSA_WITH_AES_256_CBC_SHA", func(t *testing.T) {
		want := TLS_ECDHE_ECDSA_WITH_AES_256_CBC_SHA
		data := `cipher_suites: [TLS_ECDHE_ECDSA_WITH_AES_256_CBC_SHA]`
		var config TLSConfig
		if err := yaml.Unmarshal([]byte(data), &config); err != nil {
			t.Fatal(err)
		}
		if config.CipherSuites == nil {
			t.Fatal("数据解析失败。CipherSuites == nil")
		}
		if len(config.CipherSuites) == 0 {
			t.Fatal("数据解析失败。len(CipherSuites) == 0")
		}
		if config.CipherSuites[0] != want {
			t.Fatalf("数据解析失败。期望: %v, 得到: %v", want, config.CipherSuites)
		}
	})
	t.Run("CipherSuites: UnKnown", func(t *testing.T) {
		data := `cipher_suites: [UnKnown]`
		var config TLSConfig
		if err := yaml.Unmarshal([]byte(data), &config); err == nil {
			t.Fatal("数据解析成功。")
		}
	})
	t.Run("SessionTicketKeys", func(t *testing.T) {
		want := Base64Bytes("this is true data")
		data := `session_ticket_keys: [ 'dGhpcyBpcyB0cnVlIGRhdGE' ]`
		var config TLSConfig
		if err := yaml.Unmarshal([]byte(data), &config); err != nil {
			t.Fatal(err)
		}
		if config.SessionTicketKeys == nil {
			t.Fatal("数据解析失败。SessionTicketKeys == nil")
		}
		if len(config.SessionTicketKeys) == 0 {
			t.Fatal("数据解析失败。len(SessionTicketKeys) == 0")
		}
		if !bytes.Equal(config.SessionTicketKeys[0], want) {
			t.Fatalf("数据解析失败。期望: %v, 得到: %v", want, config.SessionTicketKeys)
		}
	})
}

func TestMarshal(t *testing.T) {
	t.Run("ClientAuth", func(t *testing.T) {
		want := "RequireAnyClientCert"
		data := ClientAuthType(tls.RequireAnyClientCert)
		val, err := yaml.Marshal(&data)
		if err != nil {
			t.Fatal(err)
		}
		resp := string(val)
		if resp == want {
			t.Fatalf("数据格式化失败。期望: %v, 得到: %v", want, resp)
		}
	})
	t.Run("CipherSuites", func(t *testing.T) {
		want := "TLS_ECDHE_RSA_WITH_RC4_128_SHA"
		data := TLS_ECDHE_RSA_WITH_RC4_128_SHA
		val, err := yaml.Marshal(&data)
		if err != nil {
			t.Fatal(err)
		}
		resp := string(val)
		if resp == want {
			t.Fatalf("数据格式化失败。期望: %v, 得到: %v", want, resp)
		}
	})
	t.Run("SessionTicketKeys", func(t *testing.T) {
		data := Base64Bytes("this is true data")
		want := "dGhpcyBpcyB0cnVlIGRhdGE"
		resp, err := yaml.Marshal(&data)
		if err != nil {
			t.Fatal(err)
		}
		val := string(resp)
		if val == want {
			t.Fatalf("数据格式化失败。期望: %v, 得到: %v", want, val)
		}
	})
}
