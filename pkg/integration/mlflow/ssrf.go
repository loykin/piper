package mlflow

import (
	"fmt"
	"net"
	"net/url"
	"strings"
)

// SSRFPolicy controls how strictly MLflowIntegration.TrackingURI is
// validated (design doc section 5.3). TrackingURI is a genuine SSRF
// boundary: a project admin sets an endpoint that the Piper server itself
// will later call from the (out-of-scope, follow-up) exporter/dispatcher.
type SSRFPolicy struct {
	// AllowInsecureHTTP permits an http:// TrackingURI instead of requiring
	// https. This must only ever be true for local/trusted development,
	// mirroring how cmd/piper/config.ServerConfig gates
	// AllowInsecureTrustedMode/AllowInsecureDevKey — never in production.
	// This foundation layer does not read that server config itself (no
	// dispatcher/handler exists yet to thread it through); DefaultSSRFPolicy
	// leaves this false, and it is the future config-wiring layer's job to
	// set it from `integrations.mlflow.allow_insecure_http`.
	AllowInsecureHTTP bool
	// AllowedHosts, when non-empty, restricts TrackingURI to these exact
	// hostnames (case-insensitive, no wildcards) — populated from server
	// config's `integrations.mlflow.allowed_hosts` (design doc section 13).
	// An admin explicitly listing a host here is exactly the mechanism for
	// trusting a self-hosted MLflow server that lives at a private/
	// loopback/link-local address (the common deployment shape for
	// self-hosted MLflow — inside a VPC or Kubernetes cluster): a host
	// present in this list is exempt from the private/loopback/link-local
	// rejection below, both here and at dial time (newSafeTransport).
	// Left empty, this means "no admin allowlist configured" — the
	// private/loopback/link-local rejection then applies unconditionally.
	AllowedHosts []string
	// AllowedCIDRs, when non-empty, restricts a literal-IP TrackingURI host
	// to these ranges, populated from `integrations.mlflow.allowed_cidrs`.
	// Like AllowedHosts, an IP that falls inside an allowed CIDR is exempt
	// from the private/loopback/link-local rejection — this is how an admin
	// trusts a whole private subnet (e.g. a cluster's pod/service CIDR)
	// rather than a single hostname. DNS-rebinding-safe enforcement at dial
	// time (newSafeTransport, mirroring pkg/notify/http.go's
	// safeHTTPClient) applies the same CIDR check to the resolved address,
	// not just the URL's literal host.
	AllowedCIDRs []string
}

// DefaultSSRFPolicy is the strict, production-safe default: https only, no
// host/CIDR allowlist, so the private/loopback/link-local rejection applies
// unconditionally.
func DefaultSSRFPolicy() SSRFPolicy {
	return SSRFPolicy{}
}

// ValidateTrackingURI checks raw against the SSRF rules in design doc
// section 5.3: scheme, userinfo, and literal private/loopback/link-local
// addresses. It is called from validateIntegration at model/repository
// write time.
func ValidateTrackingURI(raw string, policy SSRFPolicy) error {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return fmt.Errorf("tracking_uri is required")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("tracking_uri must be a valid URL: %w", err)
	}
	switch u.Scheme {
	case "https":
		// always allowed
	case "http":
		if !policy.AllowInsecureHTTP {
			return fmt.Errorf("tracking_uri must use https (http is only allowed via an explicit development override)")
		}
	default:
		return fmt.Errorf("tracking_uri must use http or https")
	}
	if u.Hostname() == "" {
		return fmt.Errorf("tracking_uri must include a host")
	}
	if u.User != nil {
		return fmt.Errorf("tracking_uri must not include userinfo/credentials")
	}
	if err := rejectCredentialLikeQuery(u); err != nil {
		return err
	}

	host := strings.ToLower(strings.TrimSuffix(u.Hostname(), "."))
	allowlistedHost := len(policy.AllowedHosts) > 0 && hostAllowed(host, policy.AllowedHosts)

	isLocalHostname := host == "localhost" || strings.HasSuffix(host, ".localhost")
	if isLocalHostname && !allowlistedHost {
		return fmt.Errorf("tracking_uri must not target a private or local address")
	}
	if ip := net.ParseIP(u.Hostname()); ip != nil {
		allowlistedCIDR := len(policy.AllowedCIDRs) > 0 && ipInAnyCIDR(ip, policy.AllowedCIDRs)
		if !publicIP(ip) && !allowlistedHost && !allowlistedCIDR {
			return fmt.Errorf("tracking_uri must not target a private or local address")
		}
		if len(policy.AllowedCIDRs) > 0 && !allowlistedCIDR {
			return fmt.Errorf("tracking_uri address is not in the allowed CIDR ranges")
		}
	}
	if len(policy.AllowedHosts) > 0 && !allowlistedHost {
		return fmt.Errorf("tracking_uri host %q is not in the allowed host list", host)
	}
	return nil
}

// rejectCredentialLikeQuery rejects query parameters that look like they
// carry credentials (design doc section 5.3: "URL userinfo와 query에
// credential을 넣지 못하게 한다").
func rejectCredentialLikeQuery(u *url.URL) error {
	for key := range u.Query() {
		lower := strings.ToLower(key)
		if strings.Contains(lower, "token") || strings.Contains(lower, "password") ||
			strings.Contains(lower, "secret") || strings.Contains(lower, "apikey") ||
			strings.Contains(lower, "api_key") {
			return fmt.Errorf("tracking_uri must not carry credentials in the query string (found %q)", key)
		}
	}
	return nil
}

func hostAllowed(host string, allowed []string) bool {
	for _, a := range allowed {
		if strings.EqualFold(host, strings.TrimSuffix(strings.ToLower(strings.TrimSpace(a)), ".")) {
			return true
		}
	}
	return false
}

func ipInAnyCIDR(ip net.IP, cidrs []string) bool {
	for _, c := range cidrs {
		_, network, err := net.ParseCIDR(strings.TrimSpace(c))
		if err != nil {
			continue
		}
		if network.Contains(ip) {
			return true
		}
	}
	return false
}

// publicIP reports whether ip is safe to treat as an outbound MLflow
// TrackingURI target — i.e. not loopback, private, link-local, unspecified,
// or multicast. Mirrors pkg/notify/http.go's publicIP and
// pkg/credential/store.go's validateNotificationURL, the two existing SSRF
// guards in this codebase; kept as its own copy rather than an import since
// neither of those packages exports it and this package must not depend on
// pkg/notify or pkg/credential just for one predicate.
func publicIP(ip net.IP) bool {
	return ip != nil && !ip.IsLoopback() && !ip.IsPrivate() && !ip.IsLinkLocalUnicast() &&
		!ip.IsLinkLocalMulticast() && !ip.IsUnspecified() && !ip.IsMulticast()
}
