package runtime

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/template"

	"github.com/kreativ-anders/wharf-dev/daemon/internal/config"
	"github.com/kreativ-anders/wharf-dev/daemon/internal/configtemplate"
	"github.com/kreativ-anders/wharf-dev/daemon/internal/layout"
	"github.com/kreativ-anders/wharf-dev/daemon/internal/webserver"
)

// vhost is one project an instance serves itself: its files and its PHP.
type vhost struct {
	Name     string
	Hostname string
	DocRoot  string
	// Template is the project's rules for this webserver, before their
	// placeholders are filled in: its custom config, else its config
	// template, else Wharf's plain block (config-templates.feature).
	Template string
	// LogDir is the project's own log folder (project-logs.feature).
	LogDir   string
	SSL      bool
	CertFile string
	KeyFile  string
	PHPPort  int
	// Port is the loopback port of a project's own instance; unused by the
	// front door, which serves every project on the shared ports.
	Port int
	// Conf is the instance the vhost belongs to, for its ports and paths.
	Conf *confData
}

// forward is a project the front door hands on to the project's own
// instance, so that its URL needs no port whichever webserver serves it
// (service-management.feature, "Per-project override takes precedence over
// the global default").
type forward struct {
	Name     string
	Hostname string
	// Server is the webserver behind the front door, for the comment.
	Server string
	// LogDir is the project's own log folder: a request the front door cannot
	// hand on is the project's error too.
	LogDir   string
	Port     int
	SSL      bool
	CertFile string
	KeyFile  string
	Conf     *confData
}

type module struct{ Name, Path string }

type confData struct {
	// Front is true for the global instance, the front door: it owns ports
	// 80 and 443 on IPv4 and IPv6, serves the projects on its own webserver
	// and forwards the others. False for a project's own instance, which
	// listens on loopback only behind it.
	Front     bool
	HTTPPort  int
	HTTPSPort int
	LogDir    string
	// RunDir holds this instance's pid file and scratch files. The global
	// webserver and a project's own instance run side by side, so each needs
	// its own: Apache refuses to start over a pid file whose process lives.
	RunDir      string
	MimeTypes   string
	FastCGIConf string
	Modules     []module
	// Includes are the per-project files the main config pulls in
	// (webserver-install.feature, "One webserver, one config file per
	// project").
	Includes []string
	Vhosts   []vhost
	Forwards []forward
	AnySSL   bool
}

// rendered is a webserver instance's configuration: its main file and one
// file per project.
type rendered struct {
	Main   string
	RunDir string
	Vhosts map[string]string // path → content
	// LogDirs are the projects' log folders, which neither webserver creates.
	LogDirs []string
}

// VhostPath is where a project's generated block for one webserver lives.
// A webserver either serves a project, forwards it, or runs as its own
// instance — never two of those at once — so one file per pair suffices.
func (r *Resolver) VhostPath(server, project string) string {
	return filepath.Join(r.genDir(), server, project+".conf")
}

// renderWebserverConf generates an instance's configuration: one file per
// project, each either serving the project — its document root, PHP routed to
// the FastCGI backend for its PHP version — or, on the front door, forwarding
// it to its own instance; and a main file that includes them.
func (r *Resolver) renderWebserverConf(cfg *config.Config, in webserver.Install, instance string, front bool, served, forwarded []config.Project) (rendered, error) {
	data := &confData{
		Front:       front,
		HTTPPort:    HTTPPort,
		HTTPSPort:   HTTPSPort,
		LogDir:      forwardSlash(r.Root.LogDir()),
		RunDir:      forwardSlash(filepath.Join(r.genDir(), "run", instance)),
		FastCGIConf: forwardSlash(filepath.Join(r.genDir(), "fastcgi.conf")),
		MimeTypes:   forwardSlash(filepath.Join(r.genDir(), in.Name+"-mime.types")),
	}
	var logDirs []string
	for _, p := range served {
		docRoot := forwardSlash(ProjectDir(r.Root, p))
		logDirs = append(logDirs, r.Root.ProjectLogDir(p.Name))
		v := vhost{
			Name:     p.Name,
			Hostname: Hostname(p.Name),
			DocRoot:  docRoot,
			LogDir:   forwardSlash(r.Root.ProjectLogDir(p.Name)),
			// INFO: TLS ends at the front door; the instance behind it speaks
			// plain HTTP on loopback.
			SSL:      p.SSL && front,
			CertFile: forwardSlash(CertPath(r.Root, p.Name)),
			KeyFile:  forwardSlash(KeyPath(r.Root, p.Name)),
			PHPPort:  PHPPort(cfg.PHPVersionFor(p)),
			Port:     p.Port,
			Conf:     data,
		}
		body, err := r.ProjectRules(p, in.Name)
		if err != nil {
			return rendered{}, err
		}
		v.Template = body
		if v.SSL {
			data.AnySSL = true
		}
		data.Vhosts = append(data.Vhosts, v)
	}
	for _, p := range forwarded {
		logDirs = append(logDirs, r.Root.ProjectLogDir(p.Name))
		f := forward{
			Name:     p.Name,
			Hostname: Hostname(p.Name),
			Server:   cfg.WebserverFor(p),
			LogDir:   forwardSlash(r.Root.ProjectLogDir(p.Name)),
			Port:     p.Port,
			SSL:      p.SSL,
			CertFile: forwardSlash(CertPath(r.Root, p.Name)),
			KeyFile:  forwardSlash(KeyPath(r.Root, p.Name)),
			Conf:     data,
		}
		if f.SSL {
			data.AnySSL = true
		}
		data.Forwards = append(data.Forwards, f)
	}

	var main, fwd *template.Template
	switch in.Name {
	case webserver.Nginx:
		main, fwd = nginxTemplate, nginxForwardTemplate
	case webserver.Apache:
		main, fwd = apacheTemplate, apacheForwardTemplate
		data.Modules = apacheModules(in.Modules, data.AnySSL)
	default:
		return rendered{}, fmt.Errorf("unknown webserver %q", in.Name)
	}

	out := rendered{Vhosts: map[string]string{}, RunDir: filepath.FromSlash(data.RunDir), LogDirs: logDirs}
	add := func(name, body string) {
		path := r.VhostPath(in.Name, name)
		out.Vhosts[path] = body
		data.Includes = append(data.Includes, forwardSlash(path))
	}
	for _, v := range data.Vhosts {
		add(v.Name, siteFile(in.Name, v))
	}
	for _, f := range data.Forwards {
		var sb strings.Builder
		if err := fwd.Execute(&sb, f); err != nil {
			return rendered{}, err
		}
		add(f.Name, sb.String())
	}
	var sb strings.Builder
	if err := main.Execute(&sb, data); err != nil {
		return rendered{}, err
	}
	out.Main = sb.String()
	return out, nil
}

// write puts an instance's configuration on disk, with the support files
// every webserver install needs. Wharf writes its own mime.types and
// fastcgi_params rather than relying on the install's, because a package
// manager's copy lives wherever that package manager decided.
func (r *Resolver) write(mainPath string, conf rendered, in webserver.Install) error {
	support := map[string]string{
		filepath.Join(r.genDir(), "fastcgi.conf"):        fastcgiConf,
		filepath.Join(r.genDir(), in.Name+"-mime.types"): mimeTypes(in.Name),
	}
	for path, body := range support {
		if err := writeFile(path, body); err != nil {
			return err
		}
	}
	// WARNING: Apache's ServerRoot and nginx's temp paths live here, and neither
	// creates it.
	for _, dir := range append([]string{conf.RunDir}, conf.LogDirs...) {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	for path, body := range conf.Vhosts {
		if err := writeFile(path, body); err != nil {
			return err
		}
	}
	return writeFile(mainPath, conf.Main)
}

// apacheModules lists what the generated config loads, keeping only modules
// the install has. Builds differ: Windows compiles its MPM in and has no
// mod_unixd, distributions pick different default MPMs.
func apacheModules(dir string, ssl bool) []module {
	has := func(file string) bool { return fileExists(filepath.Join(dir, file)) }
	var out []module
	add := func(name, file string) {
		if has(file) {
			out = append(out, module{Name: name, Path: forwardSlash(filepath.Join(dir, file))})
		}
	}
	for _, mpm := range []string{"event", "worker", "prefork"} {
		if has("mod_mpm_" + mpm + ".so") {
			add("mpm_"+mpm+"_module", "mod_mpm_"+mpm+".so")
			break
		}
	}
	// WARNING: setenvif because Kirby's .htaccess uses SetEnvIf outside any
	// <IfModule>: without it every request is a 500 (webserver-install
	// .feature, "A Kirby project needs no custom webserver config").
	// proxy_http because Apache as the front door forwards to nginx.
	for _, m := range []string{"authz_core", "unixd", "dir", "mime", "log_config", "rewrite", "headers", "setenvif", "proxy", "proxy_fcgi", "proxy_http"} {
		add(m+"_module", "mod_"+m+".so")
	}
	if ssl {
		add("socache_shmcb_module", "mod_socache_shmcb.so")
		add("ssl_module", "mod_ssl.so")
	}
	return out
}

// ProjectRules is what a project is served with by one webserver: its custom
// config for that webserver where it has one, else its config template, else
// Wharf's plain block. A custom config is the user's file, edited anywhere, so
// it is checked here too: without {{listen}} it would take a port another
// project needs (app-configuration.feature, "A custom config keeps Wharf's
// placeholders").
func (r *Resolver) ProjectRules(p config.Project, server string) (string, error) {
	custom := r.Root.CustomConfig(p.Name, server)
	if body, err := os.ReadFile(custom); err == nil {
		if err := configtemplate.Check(server, string(body)); err != nil {
			return "", fmt.Errorf("%s: %w", forwardSlash(custom), err)
		}
		return string(body), nil
	} else if !os.IsNotExist(err) {
		return "", err
	}
	if p.Template == "" {
		return configtemplate.Plain(server), nil
	}
	body, err := r.Templates().Read(p.Template, server)
	if errors.Is(err, configtemplate.ErrNotFound) {
		return "", configtemplate.Missing("the config template %q does not exist — pick another in %s's settings, "+
			"or create it in Settings → Webserver", p.Template, p.Name)
	}
	return body, err
}

// Templates is the Wharf folder's config templates.
func (r *Resolver) Templates() configtemplate.Library {
	return configtemplate.Library{Dir: r.Root.TemplateDir()}
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

// CertPath and KeyPath are where mkcert output for a project is stored.
func CertPath(root layout.Root, project string) string {
	return filepath.Join(root.CertDir(), Hostname(project)+".pem")
}

func KeyPath(root layout.Root, project string) string {
	return filepath.Join(root.CertDir(), Hostname(project)+"-key.pem")
}

// renderPHPFPMConf writes a pool config listening on a TCP port. TCP rather
// than a unix socket so the same shape works on Windows (dev/architecture.md
// §4: identical behaviour over best-per-OS).
func renderPHPFPMConf(version string, port int, root layout.Root) string {
	return fmt.Sprintf(`; Generated by Wharf — do not edit; changes are overwritten on start.
[global]
error_log = %s
daemonize = no

[wharf]
listen = 127.0.0.1:%d
pm = dynamic
pm.max_children = 10
pm.start_servers = 2
pm.min_spare_servers = 1
pm.max_spare_servers = 3
clear_env = no
`, forwardSlash(filepath.Join(root.LogDir(), "php-"+version+"-fpm.log")), port)
}

const generatedHeader = `# Generated by Wharf — do not edit; changes are overwritten on start.
# The blocks below come from the project's custom config in
# config/vhosts/<project>.<webserver>.conf where it has one, else from its
# config template (Settings → Webserver).`

// siteFile is the generated file of a project an instance serves itself: its
// config template filled in once for each place the project is reachable —
// port 80, and 443 while it uses SSL, on the front door; its loopback port
// behind it. The template's header comment is left out; once per file is
// enough, and it is in the template (config-templates.feature).
//
// Each block logs to the project's own folder. PHP's warnings and errors
// arrive there too: PHP-FPM sends them to the webserver over FastCGI, which
// logs them for the block that made the request (project-logs.feature).
func siteFile(server string, v vhost) string {
	values := map[string]string{
		configtemplate.ServerName: v.Hostname,
		configtemplate.Root:       v.DocRoot,
		configtemplate.LogDir:     v.LogDir,
		configtemplate.PHP:        fmt.Sprintf("127.0.0.1:%d", v.PHPPort),
		configtemplate.FastCGI:    v.Conf.FastCGIConf,
	}
	type place struct{ listen, ssl string }
	var places []place
	switch {
	case !v.Conf.Front && server == webserver.Nginx:
		places = []place{{fmt.Sprintf("listen 127.0.0.1:%d;", v.Port), ""}}
	case !v.Conf.Front:
		places = []place{{fmt.Sprintf("127.0.0.1:%d", v.Port), ""}}
	case server == webserver.Nginx:
		places = []place{{fmt.Sprintf("listen %d;\nlisten [::]:%d;", v.Conf.HTTPPort, v.Conf.HTTPPort), ""}}
		if v.SSL {
			places = append(places, place{
				fmt.Sprintf("listen %d ssl;\nlisten [::]:%d ssl;", v.Conf.HTTPSPort, v.Conf.HTTPSPort),
				fmt.Sprintf("ssl_certificate \"%s\";\nssl_certificate_key \"%s\";", v.CertFile, v.KeyFile),
			})
		}
	default:
		places = []place{{fmt.Sprintf("*:%d", v.Conf.HTTPPort), ""}}
		if v.SSL {
			places = append(places, place{
				fmt.Sprintf("*:%d", v.Conf.HTTPSPort),
				fmt.Sprintf("SSLEngine on\nSSLCertificateFile \"%s\"\nSSLCertificateKeyFile \"%s\"", v.CertFile, v.KeyFile),
			})
		}
	}

	body := configtemplate.WithoutHeader(v.Template)
	var sb strings.Builder
	sb.WriteString(generatedHeader + "\n# " + v.Name + "\n")
	if !v.Conf.Front {
		sb.WriteString("# Behind Wharf's front door, on loopback only.\n")
	}
	for _, at := range places {
		values[configtemplate.Listen], values[configtemplate.SSL] = at.listen, at.ssl
		sb.WriteString("\n" + strings.TrimSpace(configtemplate.Fill(body, values)) + "\n")
	}
	return sb.String()
}

var nginxTemplate = template.Must(template.New("nginx").Parse(generatedHeader + `
worker_processes 1;
error_log "{{.LogDir}}/nginx-error.log";
pid "{{.RunDir}}/nginx.pid";

events { worker_connections 1024; }

http {
  include "{{.MimeTypes}}";
  default_type application/octet-stream;
  access_log "{{.LogDir}}/nginx-access.log";
  client_body_temp_path "{{.RunDir}}/body";
  proxy_temp_path "{{.RunDir}}/proxy";
  fastcgi_temp_path "{{.RunDir}}/fastcgi";
  uwsgi_temp_path "{{.RunDir}}/uwsgi";
  scgi_temp_path "{{.RunDir}}/scgi";
  sendfile on;
  # PHP enforces its own upload limit; nginx's 1 MB default would refuse a
  # Kirby Panel upload before PHP ever saw it.
  client_max_body_size 0;
{{if .Front}}
  # The front door listens on every address — macOS lets an unprivileged
  # process bind a port below 1024 nowhere else — so it answers this machine
  # only. Anyone on the network could otherwise reach a project by name, and
  # a project behind it would see the front door's 127.0.0.1 as the client.
  allow 127.0.0.0/8;
  allow ::1;
  deny all;

  # A name no project has is refused here, rather than answered by whichever
  # project happens to come first.
  server {
    listen {{.HTTPPort}} default_server;
    listen [::]:{{.HTTPPort}} default_server;
    return 404;
  }

  # The port and HTTPS PHP is told, through fastcgi.conf. On the front door
  # they are this request's own.
  map $server_port $wharf_port {
    default $server_port;
  }
  map $https $wharf_https {
    default $https;
  }
{{else}}
  # This instance sits behind Wharf's front door and listens on loopback
  # only, so the scheme and port the front door forwards can be trusted. PHP
  # must see the port the browser used, or Kirby writes this instance's own
  # port into every link.
  map $http_x_forwarded_proto $wharf_https {
    https on;
    default "";
  }
  map $http_x_forwarded_port $wharf_port {
    "" $server_port;
    default $http_x_forwarded_port;
  }
{{end}}
  # One file per project.
{{range .Includes}}  include "{{.}}";
{{end}}}
`))

const nginxForward = `{{define "forward"}}  error_log "{{.LogDir}}/error.log";
  location / {
    proxy_pass http://127.0.0.1:{{.Port}};
    proxy_http_version 1.1;
    proxy_set_header Host $host;
    proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
    proxy_set_header X-Forwarded-Proto $scheme;
    proxy_set_header X-Forwarded-Port $server_port;
  }
{{end}}`

var nginxForwardTemplate = template.Must(template.New("nginx-forward").Parse(nginxForward + generatedHeader + `
# {{.Name}} is served by its own {{.Server}} on 127.0.0.1:{{.Port}}. This is its
# front door, so its URL needs no port.

server {
  listen {{.Conf.HTTPPort}};
  listen [::]:{{.Conf.HTTPPort}};
  server_name {{.Hostname}};
{{template "forward" .}}}
{{if .SSL}}
server {
  listen {{.Conf.HTTPSPort}} ssl;
  listen [::]:{{.Conf.HTTPSPort}} ssl;
  server_name {{.Hostname}};
  ssl_certificate "{{.CertFile}}";
  ssl_certificate_key "{{.KeyFile}}";
{{template "forward" .}}}
{{end}}`))

var apacheTemplate = template.Must(template.New("apache").Parse(generatedHeader + `
ServerRoot "{{.RunDir}}"
DefaultRuntimeDir "{{.RunDir}}"
PidFile "{{.RunDir}}/httpd.pid"
ServerName localhost
{{if .Front}}Listen {{.HTTPPort}}
{{if .AnySSL}}Listen {{.HTTPSPort}}
{{end}}{{else}}{{range .Vhosts}}Listen 127.0.0.1:{{.Port}}
{{end}}{{end}}
{{range .Modules}}LoadModule {{.Name}} "{{.Path}}"
{{end}}
TypesConfig "{{.MimeTypes}}"
ErrorLog "{{.LogDir}}/apache-error.log"
LogFormat "%h %l %u %t \"%r\" %>s %b" common
CustomLog "{{.LogDir}}/apache-access.log" common
DirectoryIndex index.php index.html

# Apache appends the script's path to the handler URL. A Windows path starts
# with its drive letter, which would run into the port without the slash; this
# hands PHP the path as the OS spells it, "C:/..." on Windows and "/..."
# elsewhere (webserver-install.feature, "A Kirby project needs no custom
# webserver config"). Set here, it applies to every project's virtual host,
# whatever its config template says.
ProxyFCGISetEnvIf "reqenv('SCRIPT_FILENAME') =~ m|^proxy:fcgi://[^/]+/([A-Za-z]:)?(/.*)|" SCRIPT_FILENAME "$1$2"
{{if not .Front}}
# Behind Wharf's front door, on loopback only. PHP is told the scheme and port
# the browser used, or Kirby writes this instance's own port into every link.
ProxyFCGISetEnvIf "-n %{HTTP:X-Forwarded-Port}" SERVER_PORT "%{HTTP:X-Forwarded-Port}"
ProxyFCGISetEnvIf "%{HTTP:X-Forwarded-Proto} == 'https'" HTTPS "on"
{{end}}{{if .Front}}
# The front door listens on every address — macOS lets an unprivileged
# process bind a port below 1024 nowhere else — so it answers this machine
# only. Anyone on the network could otherwise reach a project by name, and a
# project behind it would see the front door's 127.0.0.1 as the client.
<If "! -R '127.0.0.0/8' && ! -R '::1/128'">
  Require all denied
</If>

# A name no project has is refused here, rather than answered by whichever
# project happens to come first: Apache's first virtual host is its default.
<VirtualHost *:{{.HTTPPort}}>
  ServerName localhost
  <Location "/">
    Require all denied
  </Location>
</VirtualHost>
{{end}}
# One file per project.
{{range .Includes}}Include "{{.}}"
{{end}}`))

var apacheForwardTemplate = template.Must(template.New("apache-forward").Parse(generatedHeader + `
# {{.Name}} is served by its own {{.Server}} on 127.0.0.1:{{.Port}}. This is its
# front door, so its URL needs no port.

<VirtualHost *:{{.Conf.HTTPPort}}>
  ServerName {{.Hostname}}
  ErrorLog "{{.LogDir}}/error.log"
  ProxyPreserveHost On
  ProxyPass "/" "http://127.0.0.1:{{.Port}}/"
  RequestHeader set X-Forwarded-Proto "http"
  RequestHeader set X-Forwarded-Port "{{.Conf.HTTPPort}}"
</VirtualHost>
{{if .SSL}}
<VirtualHost *:{{.Conf.HTTPSPort}}>
  ServerName {{.Hostname}}
  SSLEngine on
  SSLCertificateFile "{{.CertFile}}"
  SSLCertificateKeyFile "{{.KeyFile}}"
  ErrorLog "{{.LogDir}}/error.log"
  ProxyPreserveHost On
  ProxyPass "/" "http://127.0.0.1:{{.Port}}/"
  RequestHeader set X-Forwarded-Proto "https"
  RequestHeader set X-Forwarded-Port "{{.Conf.HTTPSPort}}"
</VirtualHost>
{{end}}`))
