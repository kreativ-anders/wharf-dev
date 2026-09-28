package core

import (
	"context"
	"fmt"
	"net/netip"

	"github.com/kreativ-anders/wharf-dev/daemon/internal/config"
	"github.com/kreativ-anders/wharf-dev/daemon/internal/lan"
	wruntime "github.com/kreativ-anders/wharf-dev/daemon/internal/runtime"
)

const noLANPort = "every port from 8800 up is taken — quit a program listening there, or remove a project"

// Share shares a running project on the local network, or stops sharing it
// (sharing.feature). A shared project gets a port of its own on this
// machine's address, which the front door opens to every device; ports 80
// and 443 keep answering this machine only. Sharing is not remembered: it
// ends with the project, and nothing is shared when Wharf starts.
func (d *Daemon) Share(ctx context.Context, name string, on bool) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	defer d.track()()

	cfg := d.store.Get()
	p, ok := cfg.Project(name)
	if !ok {
		return notFound("no project named %q", name)
	}
	if !on {
		if !d.isShared(name) {
			return nil
		}
		d.setShared(name, false)
		// INFO: The project keeps running for this machine (sharing.feature,
		// "Stopping sharing leaves the project running").
		return d.reloadGlobalWebserver(ctx, cfg)
	}
	if !d.isStarted(name) {
		return conflict("%s is not running — start it, then share it", name)
	}
	addr, err := d.lanAddress()
	if err != nil {
		return conflict("%s", lan.ErrNoNetwork)
	}
	d.setAddress(addr)
	if d.isShared(name) {
		return nil
	}
	if cfg, err = d.lanPort(cfg, p); err != nil {
		return err
	}
	d.setShared(name, true)
	if err := d.applyFrontDoor(ctx, cfg); err != nil {
		// INFO: Whatever failed — a certificate, the restart — the front door
		// comes back as it was, so the project stays up for this machine.
		d.setShared(name, false)
		if back := d.applyFrontDoor(ctx, cfg); back != nil {
			d.log.Error("front door after a failed share", "project", name, "err", back)
		}
		return err
	}
	d.log.Info("project shared on the network", "project", name, "address", addr)
	return nil
}

// lanPort gives a project its port on the network: the one recorded for it,
// unless another program holds that now, in which case the next free one,
// recorded (sharing.feature, "A shared project keeps its port").
func (d *Daemon) lanPort(cfg *config.Config, p config.Project) (*config.Config, error) {
	if p.LANPort > 0 && d.sup.PortFree(p.LANPort) {
		return cfg, nil
	}
	port := wruntime.NextLANPort(cfg, d.sup.PortFree)
	if port == 0 {
		return cfg, conflict("%s", noLANPort)
	}
	if p.LANPort > 0 {
		d.log.Info("shared port is held by another program; moving", "project", p.Name, "from", p.LANPort, "to", port)
	}
	p.LANPort = port
	return d.store.Update(func(c *config.Config) error {
		c.SetProject(p)
		return nil
	})
}

// ReplaceNetworkCertificate deletes the network certificate authority and
// its key, and serves every project shared over HTTPS with a certificate
// from a new one (sharing.feature, "Replacing the network certificate
// authority"). Every copy of the old one on a phone is then left without a
// key that could sign for it.
func (d *Daemon) ReplaceNetworkCertificate(ctx context.Context) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	defer d.track()()
	if err := d.lan.Replace(); err != nil {
		return fmt.Errorf("delete the network certificate authority: %w", err)
	}
	d.log.Info("network certificate authority replaced")
	return d.reloadGlobalWebserver(ctx, d.store.Get())
}

// CheckNetwork follows this machine's address on the local network while
// anything is shared: a new address is served at once, over HTTPS with a
// certificate for it from the same authority, so a phone needs nothing new
// (sharing.feature, "A new address needs no new certificate on the phone").
// A certificate about to run out is renewed the same way. With no address
// at all — a laptop between networks — nothing changes until one is back.
func (d *Daemon) CheckNetwork(ctx context.Context) {
	if !d.anyShared() {
		return
	}
	addr, err := d.lanAddress()
	if err != nil {
		return
	}
	if addr == d.currentAddress() && !d.certificateDue(addr) {
		return
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.currentAddress() != addr {
		d.log.Info("address on the local network changed", "from", d.currentAddress(), "to", addr)
	}
	d.setAddress(addr)
	if err := d.reloadGlobalWebserver(ctx, d.store.Get()); err != nil {
		d.log.Error("front door after a network change", "err", err)
	}
	d.publish()
}

// certificateDue reports whether a project is shared over HTTPS and the
// certificate for addr needs issuing.
func (d *Daemon) certificateDue(addr netip.Addr) bool {
	return d.anySharedSSL(d.store.Get()) && !d.lan.Current(addr)
}

// sharing is what the front door shares on the network, for the resolver.
// It issues the certificate for this machine's address while a shared
// project uses SSL, which creates the authority the first time.
func (d *Daemon) sharing(cfg *config.Config) (wruntime.Sharing, error) {
	out := wruntime.Sharing{Ports: map[string]int{}}
	addr := d.currentAddress()
	if !addr.IsValid() {
		return out, nil
	}
	out.Address = addr.String()
	ssl := false
	for _, p := range cfg.Projects {
		if d.isShared(p.Name) && p.LANPort > 0 {
			out.Ports[p.Name] = p.LANPort
			ssl = ssl || p.SSL
		}
	}
	if !ssl {
		return out, nil
	}
	cert, key, err := d.lan.Certificate(addr)
	if err != nil {
		return out, fmt.Errorf("issue the network certificate for %s: %w", addr, err)
	}
	out.CertFile, out.KeyFile, out.CADir = cert, key, d.lan.PublicDir()
	return out, nil
}

// anySharedSSL reports whether a shared project uses SSL.
func (d *Daemon) anySharedSSL(cfg *config.Config) bool {
	for _, p := range cfg.Projects {
		if p.SSL && d.isShared(p.Name) {
			return true
		}
	}
	return false
}

// Network is what the GUI shows about sharing (sharing.feature).
type Network struct {
	// Address is this machine's address on the local network, as last seen
	// while sharing; empty before anything was shared.
	Address string `json:"address,omitempty"`
	// Certificate describes the network certificate authority while a
	// project is shared over HTTPS, and CertificateURL is where a phone
	// downloads it.
	Certificate    *lan.Info `json:"certificate,omitempty"`
	CertificateURL string    `json:"certificate_url,omitempty"`
}

func (d *Daemon) networkState(cfg *config.Config) Network {
	var out Network
	addr := d.currentAddress()
	if !addr.IsValid() {
		return out
	}
	out.Address = addr.String()
	if d.anySharedSSL(cfg) {
		if info, ok := d.lan.Info(); ok {
			out.Certificate = &info
			out.CertificateURL = "http://" + out.Address + "/" + wruntime.LANCAFile
		}
	}
	return out
}

// shareURL is where a shared project is reached from another device, or
// "" while it is not shared.
func (d *Daemon) shareURL(p config.Project) string {
	addr := d.currentAddress()
	if !d.isShared(p.Name) || p.LANPort == 0 || !addr.IsValid() {
		return ""
	}
	scheme := "http"
	if p.SSL {
		scheme = "https"
	}
	return fmt.Sprintf("%s://%s:%d", scheme, addr, p.LANPort)
}

func (d *Daemon) setShared(name string, on bool) {
	d.startedMu.Lock()
	defer d.startedMu.Unlock()
	if on {
		d.shared[name] = true
	} else {
		delete(d.shared, name)
	}
}

func (d *Daemon) isShared(name string) bool {
	d.startedMu.Lock()
	defer d.startedMu.Unlock()
	return d.shared[name]
}

func (d *Daemon) anyShared() bool {
	d.startedMu.Lock()
	defer d.startedMu.Unlock()
	return len(d.shared) > 0
}

func (d *Daemon) setAddress(addr netip.Addr) {
	d.startedMu.Lock()
	defer d.startedMu.Unlock()
	d.address = addr
}

func (d *Daemon) currentAddress() netip.Addr {
	d.startedMu.Lock()
	defer d.startedMu.Unlock()
	return d.address
}
