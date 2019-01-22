// Copyright 2015 Matthew Holt
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

package certmagic

import (
	"crypto/tls"
	"fmt"
	"sync"
	"time"

	"github.com/xenolf/lego/certcrypto"
	"github.com/xenolf/lego/certificate"
	"github.com/xenolf/lego/challenge"
	"github.com/xenolf/lego/challenge/tlsalpn01"
	"github.com/xenolf/lego/lego"
)

// Config configures a certificate manager instance.
// An empty Config is not valid: use New() to obtain
// a valid Config.
type Config struct {
	// The endpoint of the directory for the ACME
	// CA we are to use
	CA string

	// The email address to use when creating or
	// selecting an existing ACME server account
	Email string

	// Set to true if agreed to the CA's
	// subscriber agreement
	Agreed bool

	// Disable all HTTP challenges
	DisableHTTPChallenge bool

	// Disable all TLS-ALPN challenges
	DisableTLSALPNChallenge bool

	// How long before expiration to renew certificates
	RenewDurationBefore time.Duration

	// How long before expiration to require a renewed
	// certificate when in interactive mode, like when
	// the program is first starting up (see
	// mholt/caddy#1680). A wider window between
	// RenewDurationBefore and this value will suppress
	// errors under duress (bad) but hopefully this duration
	// will give it enough time for the blockage to be
	// relieved.
	RenewDurationBeforeAtStartup time.Duration

	// An optional event callback clients can set
	// to subscribe to certain things happening
	// internally by this config; invocations are
	// synchronous, so make them return quickly!
	OnEvent func(event string, data interface{})

	// The host (ONLY the host, not port) to listen
	// on if necessary to start a listener to solve
	// an ACME challenge
	ListenHost string

	// The alternate port to use for the ACME HTTP
	// challenge; if non-empty,  this port will be
	// used instead of HTTPChallengePort to spin up
	// a listener for the HTTP challenge
	AltHTTPPort int

	// The alternate port to use for the ACME
	// TLS-ALPN challenge; the system must forward
	// TLSALPNChallengePort to this port for
	// challenge to succeed
	AltTLSALPNPort int

	// The DNS provider to use when solving the
	// ACME DNS challenge
	DNSProvider challenge.Provider

	// The type of key to use when generating
	// certificates
	KeyType certcrypto.KeyType

	// The maximum amount of time to allow for
	// obtaining a certificate. If empty, the
	// default from the underlying lego lib is
	// used. If set, it must not be too low so
	// as to cancel orders too early, running
	// the risk of rate limiting.
	CertObtainTimeout time.Duration

	// The state needed to operate on-demand TLS
	OnDemand *OnDemandConfig

	// Add the must staple TLS extension to the
	// CSR generated by lego/acme
	MustStaple bool

	// Map of hostname to certificate hash; used
	// to complete handshakes and serve the right
	// certificate given SNI
	certificates map[string]string

	// Pointer to the certificate store to use
	certCache *Cache

	// Map of client config key to ACME clients
	// so they can be reused
	acmeClients   map[string]*lego.Client
	acmeClientsMu *sync.Mutex
}

// NewDefault returns a new, valid, default config.
//
// Calling this function signifies your acceptance to
// the CA's Subscriber Agreement and/or Terms of Service.
func NewDefault() *Config {
	return New(Config{Agreed: true})
}

// New makes a valid config based on cfg and uses
// a default certificate cache. All calls to
// New() will use the same certificate cache.
func New(cfg Config) *Config {
	return NewWithCache(nil, cfg)
}

// NewWithCache makes a valid new config based on cfg
// and uses the provided certificate cache. If certCache
// is nil, a new, default one will be created using
// DefaultStorage; or, if a default cache has already
// been created, it will be reused.
func NewWithCache(certCache *Cache, cfg Config) *Config {
	// avoid nil pointers with sensible defaults,
	// careful to initialize a default cache (which
	// begins its maintenance goroutine) only if
	// needed - and only once (we don't initialize
	// it at package init to give importers a chance
	// to set DefaultStorage if they so desire)
	if certCache == nil {
		defaultCacheMu.Lock()
		if defaultCache == nil {
			defaultCache = NewCache(DefaultStorage)
		}
		certCache = defaultCache
		defaultCacheMu.Unlock()
	}
	if certCache.storage == nil {
		certCache.storage = DefaultStorage
	}

	// fill in default values
	if cfg.CA == "" {
		cfg.CA = CA
	}
	if cfg.Email == "" {
		cfg.Email = Email
	}
	if cfg.OnDemand == nil {
		cfg.OnDemand = OnDemand
	}
	if !cfg.Agreed {
		cfg.Agreed = Agreed
	}
	if !cfg.DisableHTTPChallenge {
		cfg.DisableHTTPChallenge = DisableHTTPChallenge
	}
	if !cfg.DisableTLSALPNChallenge {
		cfg.DisableTLSALPNChallenge = DisableTLSALPNChallenge
	}
	if cfg.RenewDurationBefore == 0 {
		cfg.RenewDurationBefore = RenewDurationBefore
	}
	if cfg.RenewDurationBeforeAtStartup == 0 {
		cfg.RenewDurationBeforeAtStartup = RenewDurationBeforeAtStartup
	}
	if cfg.OnEvent == nil {
		cfg.OnEvent = OnEvent
	}
	if cfg.ListenHost == "" {
		cfg.ListenHost = ListenHost
	}
	if cfg.AltHTTPPort == 0 {
		cfg.AltHTTPPort = AltHTTPPort
	}
	if cfg.AltTLSALPNPort == 0 {
		cfg.AltTLSALPNPort = AltTLSALPNPort
	}
	if cfg.DNSProvider == nil {
		cfg.DNSProvider = DNSProvider
	}
	if cfg.KeyType == "" {
		cfg.KeyType = KeyType
	}
	if cfg.CertObtainTimeout == 0 {
		cfg.CertObtainTimeout = CertObtainTimeout
	}
	if cfg.OnDemand == nil {
		cfg.OnDemand = OnDemand
	}
	if !cfg.MustStaple {
		cfg.MustStaple = MustStaple
	}

	// ensure the unexported fields are valid
	cfg.certificates = make(map[string]string)
	cfg.certCache = certCache
	cfg.acmeClients = make(map[string]*lego.Client)
	cfg.acmeClientsMu = new(sync.Mutex)

	return &cfg
}

// Manage causes the certificates for domainNames to be managed
// according to cfg. If cfg is enabled for OnDemand, then this
// simply whitelists the domain names. Otherwise, the certificate(s)
// for each name are loaded from storage or obtained from the CA;
// and if loaded from storage, renewed if they are expiring or
// expired. It then caches the certificate in memory and is
// prepared to serve them up during TLS handshakes.
func (cfg *Config) Manage(domainNames []string) error {
	for _, domainName := range domainNames {
		// if on-demand is configured, simply whitelist this name
		if cfg.OnDemand != nil {
			if !cfg.OnDemand.whitelistContains(domainName) {
				cfg.OnDemand.HostWhitelist = append(cfg.OnDemand.HostWhitelist, domainName)
			}
			continue
		}

		// try loading an existing certificate; if it doesn't
		// exist yet, obtain one and try loading it again
		cert, err := cfg.CacheManagedCertificate(domainName)
		if err != nil {
			if _, ok := err.(ErrNotExist); ok {
				// if it doesn't exist, get it, then try loading it again
				err := cfg.ObtainCert(domainName, false)
				if err != nil {
					return fmt.Errorf("%s: obtaining certificate: %v", domainName, err)
				}
				cert, err = cfg.CacheManagedCertificate(domainName)
				if err != nil {
					return fmt.Errorf("%s: caching certificate after obtaining it: %v", domainName, err)
				}
				continue
			}
			return fmt.Errorf("%s: caching certificate: %v", domainName, err)
		}

		// for existing certificates, make sure it is renewed
		if cert.NeedsRenewal() {
			err := cfg.RenewCert(domainName, false)
			if err != nil {
				return fmt.Errorf("%s: renewing certificate: %v", domainName, err)
			}
		}
	}

	return nil
}

// ObtainCert obtains a certificate for name using cfg, as long
// as a certificate does not already exist in storage for that
// name. The name must qualify and cfg must be flagged as Managed.
// This function is a no-op if storage already has a certificate
// for name.
//
// It only obtains and stores certificates (and their keys),
// it does not load them into memory. If interactive is true,
// the user may be shown a prompt.
func (cfg *Config) ObtainCert(name string, interactive bool) error {
	skip, err := cfg.preObtainOrRenewChecks(name, interactive)
	if err != nil {
		return err
	}
	if skip {
		return nil
	}

	if cfg.storageHasCertResources(name) {
		return nil
	}

	client, err := cfg.newACMEClient(interactive)
	if err != nil {
		return err
	}

	return client.Obtain(name)
}

// RenewCert renews the certificate for name using cfg. It stows the
// renewed certificate and its assets in storage if successful.
func (cfg *Config) RenewCert(name string, interactive bool) error {
	skip, err := cfg.preObtainOrRenewChecks(name, interactive)
	if err != nil {
		return err
	}
	if skip {
		return nil
	}
	client, err := cfg.newACMEClient(interactive)
	if err != nil {
		return err
	}
	return client.Renew(name)
}

// RevokeCert revokes the certificate for domain via ACME protocol.
func (cfg *Config) RevokeCert(domain string, interactive bool) error {
	client, err := cfg.newACMEClient(interactive)
	if err != nil {
		return err
	}
	return client.Revoke(domain)
}

// TLSConfig is an opinionated method that returns a
// recommended, modern TLS configuration that can be
// used to configure TLS listeners, which also supports
// the TLS-ALPN challenge and serves up certificates
// managed by cfg.
//
// Unlike the package TLS() function, this method does
// not, by itself, enable certificate management for
// any domain names.
//
// Feel free to further customize the returned tls.Config,
// but do not mess with the GetCertificate or NextProtos
// fields unless you know what you're doing, as they're
// necessary to solve the TLS-ALPN challenge.
func (cfg *Config) TLSConfig() *tls.Config {
	return &tls.Config{
		// these two fields necessary for TLS-ALPN challenge
		GetCertificate: cfg.GetCertificate,
		NextProtos:     []string{"h2", "http/1.1", tlsalpn01.ACMETLS1Protocol},

		// the rest recommended for modern TLS servers
		MinVersion: tls.VersionTLS12,
		CurvePreferences: []tls.CurveID{
			tls.X25519,
			tls.CurveP256,
		},
		CipherSuites:             preferredDefaultCipherSuites(),
		PreferServerCipherSuites: true,
	}
}

// preObtainOrRenewChecks perform a few simple checks before
// obtaining or renewing a certificate with ACME, and returns
// whether this name should be skipped (like if it's not
// managed TLS) as well as any error. It ensures that the
// config is Managed, that the name qualifies for a certificate,
// and that an email address is available.
func (cfg *Config) preObtainOrRenewChecks(name string, allowPrompts bool) (bool, error) {
	if !HostQualifies(name) {
		return true, nil
	}

	err := cfg.getEmail(allowPrompts)
	if err != nil {
		return false, err
	}

	return false, nil
}

// storageHasCertResources returns true if the storage
// associated with cfg's certificate cache has all the
// resources related to the certificate for domain: the
// certificate, the private key, and the metadata.
func (cfg *Config) storageHasCertResources(domain string) bool {
	certKey := StorageKeys.SiteCert(cfg.CA, domain)
	keyKey := StorageKeys.SitePrivateKey(cfg.CA, domain)
	metaKey := StorageKeys.SiteMeta(cfg.CA, domain)
	return cfg.certCache.storage.Exists(certKey) &&
		cfg.certCache.storage.Exists(keyKey) &&
		cfg.certCache.storage.Exists(metaKey)
}

// managedCertNeedsRenewal returns true if certRes is
// expiring soon or already expired, or if the process
// of checking the expiration returned an error.
func (cfg *Config) managedCertNeedsRenewal(certRes certificate.Resource) bool {
	cert, err := cfg.makeCertificate(certRes.Certificate, certRes.PrivateKey)
	if err != nil {
		return true
	}
	return cert.NeedsRenewal()
}
