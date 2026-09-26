package core

import (
	"crypto/x509"
	"encoding/pem"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/kreativ-anders/wharf-dev/daemon/internal/ipc"
)

// started adds and starts projects, the way a user has them running.
func (h *harness) started(names ...string) {
	h.t.Helper()
	for _, name := range names {
		h.mustAdd(name)
		if err := h.d.StartProject(h.ctx(), name); err != nil {
			h.t.Fatalf("start %s: %v", name, err)
		}
	}
}

func (h *harness) share(name string) {
	h.t.Helper()
	if err := h.d.Share(h.ctx(), name, true); err != nil {
		h.t.Fatalf("share %s: %v", name, err)
	}
}

func (h *harness) sslOn(name string) {
	h.t.Helper()
	on := true
	if _, err := h.d.UpdateSettings(h.ctx(), name, Settings{SSL: &on}); err != nil {
		h.t.Fatalf("SSL for %s: %v", name, err)
	}
}

func (h *harness) lanPort(name string) int {
	h.t.Helper()
	p, _ := h.d.Config().Project(name)
	return p.LANPort
}

// servedCert is the certificate a generated config names on a line, parsed.
func servedCert(t *testing.T, conf, directive string) *x509.Certificate {
	t.Helper()
	for _, line := range strings.Split(conf, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, directive+" ") {
			continue
		}
		path := strings.Trim(strings.TrimSuffix(strings.TrimPrefix(line, directive+" "), ";"), `"`)
		raw, err := os.ReadFile(filepath.FromSlash(path))
		if err != nil {
			t.Fatal(err)
		}
		block, _ := pem.Decode(raw)
		if block == nil {
			t.Fatalf("%s holds no certificate", path)
		}
		cert, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			t.Fatal(err)
		}
		return cert
	}
	t.Fatalf("no %s line in:\n%s", directive, conf)
	return nil
}

// features/sharing.feature — "Sharing a running project on the network"
func TestSharingARunningProjectOnTheNetwork(t *testing.T) {
	h := newHarness(t)
	h.started("my-kirby-site")
	h.share("my-kirby-site")

	// INFO: Then the front door serves "my-kirby-site" to every device on the
	// network at "http://192.168.1.23:8800"
	if got := h.project("my-kirby-site"); !got.Shared || got.ShareURL != "http://192.168.1.23:8800" {
		t.Fatalf("shared = %v at %q, want http://192.168.1.23:8800", got.Shared, got.ShareURL)
	}
	conf := h.readGenerated("nginx.conf")
	block := "# Shared on the network: every device may reach this block.\nserver {\n  listen 8800;\n  allow all;"
	if !strings.Contains(conf, block) || !strings.Contains(conf, "server_name 192.168.1.23;") {
		t.Fatalf("the front door does not open port 8800 to every device:\n%s", conf)
	}
	// INFO: And PHP is told the host and port the phone used: the block is
	// the project's own, so PHP gets the Host header as sent and the port
	// through $wharf_port, which is the request's own port.
	if !strings.Contains(conf, "map $server_port $wharf_port") {
		t.Fatalf("PHP is not told the port the request came in on:\n%s", conf)
	}

	// INFO: And "my-kirby-site.localhost" still answers this machine only: the
	// rule stands ahead of every block, and only the shared one lifts it.
	if !strings.Contains(conf, "allow 127.0.0.0/8;\n  allow ::1;\n  deny all;") || strings.Count(conf, "allow all;") != 1 {
		t.Fatalf("the front door answers other machines beyond the shared port:\n%s", conf)
	}

	// INFO: The same with Apache at the front door: the shared virtual host
	// marks its requests, and the rule lets marked requests alone through.
	if err := h.d.SetWebserver(h.ctx(), "apache"); err != nil {
		t.Fatal(err)
	}
	apache := h.readGenerated("apache.conf")
	for _, want := range []string{
		"Listen 8800\n",
		"<VirtualHost *:8800>\n  " + `SetEnvIf Remote_Addr "." WHARF_SHARED`,
		"ServerName 192.168.1.23",
		`<If "! -R '127.0.0.0/8' && ! -R '::1/128' && -z reqenv('WHARF_SHARED')">`,
	} {
		if !strings.Contains(apache, want) {
			t.Fatalf("Apache does not share the project on port 8800 (missing %q):\n%s", want, apache)
		}
	}
	if strings.Count(apache, "WHARF_SHARED\n") != 1 || strings.Contains(apache, "Listen 443") {
		t.Fatalf("Apache opens more than the shared port:\n%s", apache)
	}
}

// features/sharing.feature — "A shared project keeps its port"
func TestASharedProjectKeepsItsPort(t *testing.T) {
	h := newHarness(t)
	h.started("my-kirby-site", "other-site")
	h.share("my-kirby-site")
	h.share("other-site")
	if h.lanPort("my-kirby-site") != 8800 || h.lanPort("other-site") != 8801 {
		t.Fatalf("ports %d and %d, want 8800 and 8801", h.lanPort("my-kirby-site"), h.lanPort("other-site"))
	}

	// INFO: Given it was shared on port 8800 before, When it is shared again
	// after Wharf restarted: the port is in wharf.json, and nothing is
	// shared at a start.
	raw, err := os.ReadFile(h.root.ConfigFile())
	if err != nil || !strings.Contains(string(raw), `"lan_port": 8800`) {
		t.Fatalf("wharf.json does not record the port (%v):\n%s", err, raw)
	}
	restarted := newHarness(t, func(o *Options) { o.Root = h.root; o.Store = nil })
	restarted.root = h.root
	if restarted.project("my-kirby-site").Shared {
		t.Fatal("a project is shared right after a start")
	}
	if err := restarted.d.StartProject(restarted.ctx(), "my-kirby-site"); err != nil {
		t.Fatal(err)
	}
	restarted.share("my-kirby-site")
	if got := restarted.project("my-kirby-site").ShareURL; got != "http://192.168.1.23:8800" {
		t.Fatalf("shared again at %q, want port 8800 again", got)
	}

	// INFO: And if another program holds that port, it moves to the next free
	// one and wharf.json records that — past the port another project has.
	if err := restarted.d.Share(restarted.ctx(), "my-kirby-site", false); err != nil {
		t.Fatal(err)
	}
	restarted.ports.Bind(8800)
	restarted.share("my-kirby-site")
	if got := restarted.lanPort("my-kirby-site"); got != 8802 {
		t.Fatalf("moved to port %d, want 8802", got)
	}
	if !strings.Contains(restarted.readGenerated("nginx.conf"), "listen 8802;") {
		t.Fatal("the front door does not listen on the new port")
	}
}

// features/sharing.feature — "Sharing ends with the project"
func TestSharingEndsWithTheProject(t *testing.T) {
	h := newHarness(t)
	h.started("my-kirby-site", "other-site")
	h.share("my-kirby-site")

	// INFO: When the user stops it, Then its port on the network is closed
	if err := h.d.StopProject(h.ctx(), "my-kirby-site"); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(h.readGenerated("nginx.conf"), "listen 8800") {
		t.Fatal("port 8800 is still open once the project stopped")
	}
	// INFO: And starting it again does not share it again
	if err := h.d.StartProject(h.ctx(), "my-kirby-site"); err != nil {
		t.Fatal(err)
	}
	if h.project("my-kirby-site").Shared || strings.Contains(h.readGenerated("nginx.conf"), "listen 8800") {
		t.Fatal("a restarted project is shared again without being asked")
	}

	// INFO: The same for "Stop all", and a project whose start failed.
	h.share("my-kirby-site")
	if err := h.d.StopAll(h.ctx()); err != nil {
		t.Fatal(err)
	}
	if h.d.anyShared() {
		t.Fatal("a project is still shared after Stop all")
	}
}

// features/sharing.feature — "Stopping sharing leaves the project running"
func TestStoppingSharingLeavesTheProjectRunning(t *testing.T) {
	h := newHarness(t)
	h.started("my-kirby-site")
	h.share("my-kirby-site")

	if err := h.d.Share(h.ctx(), "my-kirby-site", false); err != nil {
		t.Fatal(err)
	}
	conf := h.readGenerated("nginx.conf")
	if strings.Contains(conf, "listen 8800") || strings.Contains(conf, "allow all;") {
		t.Fatalf("port 8800 is still open:\n%s", conf)
	}
	if got := h.project("my-kirby-site"); got.State != "running" || got.Shared || !strings.Contains(conf, "server_name my-kirby-site.localhost;") {
		t.Fatalf("my-kirby-site is %s, shared %v, after sharing stopped", got.State, got.Shared)
	}
}

// features/sharing.feature — "Only a running project can be shared"
func TestOnlyARunningProjectCanBeShared(t *testing.T) {
	h := newHarness(t)
	h.mustAdd("my-kirby-site")

	err := h.d.Share(h.ctx(), "my-kirby-site", true)
	var ipcErr *ipc.Error
	if !errors.As(asIPCError(err), &ipcErr) || ipcErr.Code != ipc.CodeConflict || !strings.Contains(err.Error(), "start it") {
		t.Fatalf("sharing a stopped project: %v, want a conflict that says to start it", err)
	}
	if h.project("my-kirby-site").Shared || h.lanPort("my-kirby-site") != 0 {
		t.Fatal("a stopped project was shared")
	}
}

// features/sharing.feature — "Sharing needs a local network"
func TestSharingNeedsALocalNetwork(t *testing.T) {
	h := newHarness(t)
	h.started("my-kirby-site")
	h.net.set("")

	err := h.d.Share(h.ctx(), "my-kirby-site", true)
	var ipcErr *ipc.Error
	if !errors.As(asIPCError(err), &ipcErr) || ipcErr.Code != ipc.CodeConflict || !strings.Contains(err.Error(), "connect to one") {
		t.Fatalf("sharing without a network: %v, want a conflict that says to connect", err)
	}
	if h.project("my-kirby-site").Shared || strings.Contains(h.readGenerated("nginx.conf"), "allow all;") {
		t.Fatal("the project was shared without a network")
	}
}

// features/sharing.feature — "A project on the other webserver is shared
// through the front door"
func TestAProjectOnTheOtherWebserverIsSharedThroughTheFrontDoor(t *testing.T) {
	h := newHarness(t)
	h.mustAdd("legacy-app")
	apache := "apache"
	if _, err := h.d.UpdateSettings(h.ctx(), "legacy-app", Settings{Webserver: &apache}); err != nil {
		t.Fatal(err)
	}
	if err := h.d.StartProject(h.ctx(), "legacy-app"); err != nil {
		t.Fatal(err)
	}
	h.share("legacy-app")
	p, _ := h.d.Config().Project("legacy-app")

	// INFO: Then the front door forwards its port on the network to
	// "legacy-app"'s own apache — with the Host header as the phone sent it,
	// port included, and the port it used.
	conf := h.readGenerated("nginx.conf")
	for _, want := range []string{
		"listen 8800;\n  allow all;",
		"proxy_pass http://127.0.0.1:" + strconv.Itoa(p.Port) + ";",
		"proxy_set_header Host $http_host;",
		"proxy_set_header X-Forwarded-Port $server_port;",
	} {
		if !strings.Contains(conf, want) {
			t.Fatalf("the front door does not forward the shared port (missing %q):\n%s", want, conf)
		}
	}
	// INFO: And that apache tells PHP the port the phone used
	own := h.readGenerated("project-legacy-app-apache.conf")
	if !strings.Contains(own, `ProxyFCGISetEnvIf "-n %{HTTP:X-Forwarded-Port}" SERVER_PORT`) {
		t.Fatalf("legacy-app's own apache does not pass the forwarded port on:\n%s", own)
	}

	// INFO: With Apache at the front door, it forwards the same way.
	if err := h.d.SetWebserver(h.ctx(), "apache"); err != nil {
		t.Fatal(err)
	}
	if err := h.d.Share(h.ctx(), "legacy-app", false); err != nil {
		t.Fatal(err)
	}
	nginx := "nginx"
	if _, err := h.d.UpdateSettings(h.ctx(), "legacy-app", Settings{Webserver: &nginx}); err != nil {
		t.Fatal(err)
	}
	h.share("legacy-app")
	front := h.readGenerated("apache.conf")
	for _, want := range []string{
		"<VirtualHost *:8800>\n  SetEnvIf Remote_Addr \".\" WHARF_SHARED",
		`RequestHeader set X-Forwarded-Port "8800"`,
		"ProxyPreserveHost On",
	} {
		if !strings.Contains(front, want) {
			t.Fatalf("Apache does not forward the shared port (missing %q):\n%s", want, front)
		}
	}
}

// features/sharing.feature — "Sharing a project with SSL over HTTPS"
func TestSharingAProjectWithSSLOverHTTPS(t *testing.T) {
	h := newHarness(t)
	h.started("my-kirby-site")
	h.sslOn("my-kirby-site")
	h.share("my-kirby-site")

	// INFO: Then the front door serves it at "https://192.168.1.23:8800" with a
	// certificate for "192.168.1.23"
	if got := h.project("my-kirby-site").ShareURL; got != "https://192.168.1.23:8800" {
		t.Fatalf("shared at %q", got)
	}
	conf := h.readGenerated("nginx.conf")
	shared := conf[strings.Index(conf, "listen 8800 ssl;"):]
	cert := servedCert(t, shared, "ssl_certificate")
	if len(cert.IPAddresses) != 1 || !cert.IPAddresses[0].Equal(net.ParseIP("192.168.1.23")) {
		t.Fatalf("the shared port serves a certificate for %v", cert.IPAddresses)
	}
	// INFO: And that certificate comes from Wharf's network certificate
	// authority — not mkcert's.
	if !strings.HasPrefix(filepath.ToSlash(filepath.Dir(servedCertPath(shared))), filepath.ToSlash(h.root.NetworkCADir())) {
		t.Fatalf("the certificate is not the network certificate authority's: %s", servedCertPath(shared))
	}

	// INFO: And the authority's certificate — never its key — can be downloaded
	// at "http://192.168.1.23/wharf-network-ca.crt"
	offer := h.d.State().Network
	if offer.CertificateURL != "http://192.168.1.23/wharf-network-ca.crt" || offer.Certificate == nil || offer.Certificate.Fingerprint == "" {
		t.Fatalf("the snapshot offers %+v", offer)
	}
	if !strings.Contains(conf, "location = /wharf-network-ca.crt {\n      allow all;") {
		t.Fatalf("the front door does not offer the authority on port 80:\n%s", conf)
	}
	offered := filepath.Join(filepath.FromSlash(between(conf, "location = /wharf-network-ca.crt {", `root "`, `";`)), "wharf-network-ca.crt")
	entries, err := os.ReadDir(filepath.Dir(offered))
	if err != nil || len(entries) != 1 {
		t.Fatalf("the offered folder holds %v (%v), not the certificate alone", entries, err)
	}
	if err := h.d.SetWebserver(h.ctx(), "apache"); err != nil {
		t.Fatal(err)
	}
	apache := h.readGenerated("apache.conf")
	for _, want := range []string{
		"Listen 8800 https",
		`SetEnvIf Request_URI "^/wharf-network-ca\.crt$" WHARF_SHARED`,
		"ForceType application/x-x509-ca-cert",
	} {
		if !strings.Contains(apache, want) {
			t.Fatalf("Apache does not share over HTTPS (missing %q):\n%s", want, apache)
		}
	}
}

// servedCertPath is the path an nginx ssl_certificate line names.
func servedCertPath(conf string) string {
	return between(conf, "", `ssl_certificate "`, `";`)
}

// between returns the text after start, from the first open to the next
// close.
func between(s, start, open, close string) string {
	s = s[strings.Index(s, start):]
	s = s[strings.Index(s, open)+len(open):]
	return s[:strings.Index(s, close)]
}

// features/sharing.feature — "The certificate step is shown only for a
// project with SSL"
func TestTheNetworkCertificateIsOfferedOnlyWhileSharedOverHTTPS(t *testing.T) {
	h := newHarness(t)
	h.started("my-kirby-site")
	h.share("my-kirby-site")
	if offer := h.d.State().Network; offer.Certificate != nil || offer.CertificateURL != "" {
		t.Fatalf("a project without SSL offers a certificate: %+v", offer)
	}
	if strings.Contains(h.readGenerated("nginx.conf"), "wharf-network-ca.crt") {
		t.Fatal("the front door offers the authority with nothing shared over HTTPS")
	}
	if _, err := os.Stat(h.root.NetworkCADir()); !os.IsNotExist(err) {
		t.Fatal("sharing without SSL created the network certificate authority")
	}
}

// features/sharing.feature — "A new address needs no new certificate on the
// phone"
func TestANewAddressIsServedWithoutANewAuthority(t *testing.T) {
	h := newHarness(t)
	h.started("my-kirby-site")
	h.sslOn("my-kirby-site")
	h.share("my-kirby-site")
	before := h.d.State().Network.Certificate.Fingerprint

	// INFO: When this machine's address on the local network changes
	h.net.set("192.168.1.42")
	h.d.CheckNetwork(h.ctx())

	// INFO: Then Wharf issues a certificate for "192.168.1.42" from the same
	// authority, And the front door serves "my-kirby-site" at
	// "https://192.168.1.42:8800"
	conf := h.readGenerated("nginx.conf")
	cert := servedCert(t, conf[strings.Index(conf, "listen 8800 ssl;"):], "ssl_certificate")
	if !cert.IPAddresses[0].Equal(net.ParseIP("192.168.1.42")) || !strings.Contains(conf, "server_name 192.168.1.42;") {
		t.Fatalf("the front door serves %v:\n%s", cert.IPAddresses, conf)
	}
	if got := h.project("my-kirby-site").ShareURL; got != "https://192.168.1.42:8800" {
		t.Fatalf("shared at %q", got)
	}
	if after := h.d.State().Network.Certificate.Fingerprint; after != before {
		t.Fatal("a new address replaced the authority the phone trusts")
	}

	// INFO: Without an address — between networks — the last one stays.
	h.net.set("")
	h.d.CheckNetwork(h.ctx())
	if got := h.project("my-kirby-site").ShareURL; got != "https://192.168.1.42:8800" {
		t.Fatalf("shared at %q after the network went away", got)
	}
}

// features/sharing.feature — "Replacing the network certificate authority"
func TestReplacingTheNetworkCertificateAuthority(t *testing.T) {
	h := newHarness(t)
	h.started("my-kirby-site")
	h.sslOn("my-kirby-site")
	h.share("my-kirby-site")
	before := h.d.State().Network.Certificate.Fingerprint
	restarts := len(h.runner.Started)

	if err := h.d.ReplaceNetworkCertificate(h.ctx()); err != nil {
		t.Fatal(err)
	}
	// INFO: Then a new authority is created, And every project shared over HTTPS
	// is served with a certificate from the new authority.
	after := h.d.State().Network.Certificate
	if after == nil || after.Fingerprint == before {
		t.Fatalf("the authority was not replaced: %+v", after)
	}
	if len(h.runner.Started) == restarts {
		t.Fatal("the front door kept serving the old authority's certificate")
	}
	raw, err := os.ReadFile(after.File)
	if err != nil {
		t.Fatal(err)
	}
	ca, err := x509.ParseCertificate(raw)
	if err != nil {
		t.Fatal(err)
	}
	conf := h.readGenerated("nginx.conf")
	if err := servedCert(t, conf[strings.Index(conf, "listen 8800 ssl;"):], "ssl_certificate").CheckSignatureFrom(ca); err != nil {
		t.Fatalf("the shared port is not served with the new authority's certificate: %v", err)
	}
}

// features/sharing.feature — "Resetting Wharf retires the network certificate
// authority"
func TestResettingWharfRetiresTheNetworkCertificateAuthority(t *testing.T) {
	h := newHarness(t)
	h.started("my-kirby-site")
	h.sslOn("my-kirby-site")
	h.share("my-kirby-site")
	key := filepath.Join(h.root.NetworkCADir(), "ca-key.pem")
	if _, err := os.Stat(key); err != nil {
		t.Fatalf("no authority to retire: %v", err)
	}

	if err := h.d.Reset(h.ctx(), false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(h.root.NetworkCADir()); !os.IsNotExist(err) {
		t.Fatalf("the network certificate authority survived a reset: %v", err)
	}
}
