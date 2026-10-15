package direct

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"slices"
	"time"

	"golang.org/x/crypto/acme"
)

// certStore keeps this server's wildcard cert in <data>/certs/direct.
type certStore struct{ dir string }

func (s certStore) path(name string) string { return filepath.Join(s.dir, name) }

// load returns the stored cert if it covers *.<domain>.
func (s certStore) load(domain string) (*tls.Certificate, error) {
	c, err := tls.LoadX509KeyPair(s.path("cert.pem"), s.path("key.pem"))
	if err != nil {
		return nil, err
	}
	if c.Leaf == nil {
		if c.Leaf, err = x509.ParseCertificate(c.Certificate[0]); err != nil {
			return nil, err
		}
	}
	if !slices.Contains(c.Leaf.DNSNames, "*."+domain) {
		return nil, errors.New("stored cert is for another name")
	}
	return &c, nil
}

func (s certStore) accountKey() (crypto.Signer, error) {
	if b, err := os.ReadFile(s.path("account.key")); err == nil {
		if blk, _ := pem.Decode(b); blk != nil {
			return x509.ParseECPrivateKey(blk.Bytes)
		}
	}
	k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	der, _ := x509.MarshalECPrivateKey(k)
	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		return nil, err
	}
	return k, writeFile(s.path("account.key"), pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: der}))
}

func (s certStore) save(chain [][]byte, key *ecdsa.PrivateKey) error {
	var certPEM []byte
	for _, der := range chain {
		certPEM = append(certPEM, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})...)
	}
	kd, _ := x509.MarshalECPrivateKey(key)
	if err := writeFile(s.path("key.pem"), pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: kd})); err != nil {
		return err
	}
	return writeFile(s.path("cert.pem"), certPEM)
}

func writeFile(path string, b []byte) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// needsRenewal: renew once less than a third of the lifetime is left.
func needsRenewal(c *tls.Certificate, now time.Time) bool {
	if c == nil || c.Leaf == nil {
		return true
	}
	life := c.Leaf.NotAfter.Sub(c.Leaf.NotBefore)
	return c.Leaf.NotAfter.Sub(now) < life/3
}

// issue gets a wildcard cert for *.<domain> from the ACME CA using a DNS-01
// challenge that the wrentv-direct API publishes for us.
func (m *Manager) issue(ctx context.Context, domain string) (*tls.Certificate, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	key, err := m.store.accountKey()
	if err != nil {
		return nil, err
	}
	client := &acme.Client{Key: key, DirectoryURL: m.acmeDirectory(), UserAgent: "wrentv"}
	if _, err := client.Register(ctx, &acme.Account{}, acme.AcceptTOS); err != nil && !errors.Is(err, acme.ErrAccountAlreadyExists) {
		return nil, fmt.Errorf("acme account: %w", err)
	}
	order, err := client.AuthorizeOrder(ctx, acme.DomainIDs("*."+domain))
	if err != nil {
		return nil, fmt.Errorf("acme order: %w", err)
	}

	type pending struct {
		chal  *acme.Challenge
		authz string
	}
	var todo []pending
	var values []string
	for _, u := range order.AuthzURLs {
		az, err := client.GetAuthorization(ctx, u)
		if err != nil {
			return nil, err
		}
		if az.Status == acme.StatusValid {
			continue
		}
		var chal *acme.Challenge
		for _, c := range az.Challenges {
			if c.Type == "dns-01" {
				chal = c
			}
		}
		if chal == nil {
			return nil, errors.New("acme: no dns-01 challenge offered")
		}
		v, err := client.DNS01ChallengeRecord(chal.Token)
		if err != nil {
			return nil, err
		}
		todo = append(todo, pending{chal, az.URI})
		values = append(values, v)
	}

	if len(todo) > 0 {
		nameservers, err := m.api.setACME(ctx, values)
		if err != nil {
			return nil, fmt.Errorf("publish challenge: %w", err)
		}
		defer func() {
			c, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			_, _ = m.api.setACME(c, nil)
		}()
		if err := waitTXT(ctx, "_acme-challenge."+domain, values, nameservers); err != nil {
			return nil, err
		}
		for _, p := range todo {
			if _, err := client.Accept(ctx, p.chal); err != nil {
				return nil, fmt.Errorf("acme accept: %w", err)
			}
			if _, err := client.WaitAuthorization(ctx, p.authz); err != nil {
				return nil, fmt.Errorf("acme validation: %w", err)
			}
		}
	}

	if order, err = client.WaitOrder(ctx, order.URI); err != nil {
		return nil, fmt.Errorf("acme order: %w", err)
	}
	certKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	csr, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{DNSNames: []string{"*." + domain}}, certKey)
	if err != nil {
		return nil, err
	}
	chain, _, err := client.CreateOrderCert(ctx, order.FinalizeURL, csr, true)
	if err != nil {
		return nil, fmt.Errorf("acme finalize: %w", err)
	}
	if err := m.store.save(chain, certKey); err != nil {
		return nil, err
	}
	slog.Info("got HTTPS certificate", "name", "*."+domain)
	return m.store.load(domain)
}

// waitTXT polls the zone's own nameservers until every value is visible, so
// the CA's lookup doesn't race the DNS update.
func waitTXT(ctx context.Context, name string, values, nameservers []string) error {
	if len(nameservers) == 0 {
		nameservers = []string{"1.1.1.1"}
	}
	resolvers := make([]*net.Resolver, len(nameservers))
	for i, ns := range nameservers {
		addr := net.JoinHostPort(ns, "53")
		resolvers[i] = &net.Resolver{PreferGo: true, Dial: func(ctx context.Context, network, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, network, addr)
		}}
	}
	t := time.NewTicker(3 * time.Second)
	defer t.Stop()
	for {
		ok := true
		for _, r := range resolvers {
			got, _ := r.LookupTXT(ctx, name)
			for _, v := range values {
				if !slices.Contains(got, v) {
					ok = false
				}
			}
		}
		if ok {
			return nil
		}
		select {
		case <-ctx.Done():
			return errors.New("timed out waiting for the challenge record to appear in DNS")
		case <-t.C:
		}
	}
}
