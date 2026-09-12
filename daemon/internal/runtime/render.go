package runtime

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"text/template"

	"github.com/kreativ-anders/wharf-dev/daemon/internal/config"
	"github.com/kreativ-anders/wharf-dev/daemon/internal/layout"
	"github.com/kreativ-anders/wharf-dev/daemon/internal/webserver"
)

// vhost is one project an instance serves itself: its files and its PHP.
type vhost struct {
	Name     string
	Hostname string
	DocRoot  string
	// DocRootRe is DocRoot escaped for Apache's regex-matched sections: a
	// folder added from elsewhere can contain any character.
	DocRootRe string
	// Include is the project's custom directives for this webserver, or ""
	// when it has none (app-configuration.feature).
	Include string
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
	RunDir        string
	MimeTypes     string
	FastCGIParams string
	Modules       []module
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
		Front:         front,
		HTTPPort:      HTTPPort,
		HTTPSPort:     HTTPSPort,
		LogDir:        forwardSlash(r.Root.LogDir()),
		RunDir:        forwardSlash(filepath.Join(r.genDir(), "run", instance)),
		FastCGIParams: forwardSlash(filepath.Join(r.genDir(), "fastcgi_params")),
		MimeTypes:     forwardSlash(filepath.Join(r.genDir(), in.Name+"-mime.types")),
	}
	var logDirs []string
	for _, p := range served {
		docRoot := forwardSlash(ProjectDir(r.Root, p))
		logDirs = append(logDirs, r.Root.ProjectLogDir(p.Name))
		v := vhost{
			Name:      p.Name,
			Hostname:  Hostname(p.Name),
			DocRoot:   docRoot,
			DocRootRe: regexp.QuoteMeta(docRoot),
			LogDir:    forwardSlash(r.Root.ProjectLogDir(p.Name)),
			// TLS ends at the front door; the instance behind it speaks
			// plain HTTP on loopback.
			SSL:      p.SSL && front,
			CertFile: forwardSlash(CertPath(r.Root, p.Name)),
			KeyFile:  forwardSlash(KeyPath(r.Root, p.Name)),
			PHPPort:  PHPPort(cfg, cfg.PHPVersionFor(p)),
			Port:     p.Port,
			Conf:     data,
		}
		// Only the file for the webserver actually serving the project is
		// included; the other one waits until the project switches.
		if custom := r.Root.CustomConfig(p.Name, in.Name); fileExists(custom) {
			v.Include = forwardSlash(custom)
		}
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

	var main, host, fwd *template.Template
	switch in.Name {
	case webserver.Nginx:
		main, host, fwd = nginxTemplate, nginxVhostTemplate, nginxForwardTemplate
	case webserver.Apache:
		main, host, fwd = apacheTemplate, apacheVhostTemplate, apacheForwardTemplate
		data.Modules = apacheModules(in.Modules, data.AnySSL)
	default:
		return rendered{}, fmt.Errorf("unknown webserver %q", in.Name)
	}

	out := rendered{Vhosts: map[string]string{}, RunDir: filepath.FromSlash(data.RunDir), LogDirs: logDirs}
	add := func(name string, tpl *template.Template, v any) error {
		var sb strings.Builder
		if err := tpl.Execute(&sb, v); err != nil {
			return err
		}
		path := r.VhostPath(in.Name, name)
		out.Vhosts[path] = sb.String()
		data.Includes = append(data.Includes, forwardSlash(path))
		return nil
	}
	for _, v := range data.Vhosts {
		if err := add(v.Name, host, v); err != nil {
			return rendered{}, err
		}
	}
	for _, f := range data.Forwards {
		if err := add(f.Name, fwd, f); err != nil {
			return rendered{}, err
		}
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
		filepath.Join(r.genDir(), "fastcgi_params"):      fastcgiParams,
		filepath.Join(r.genDir(), in.Name+"-mime.types"): mimeTypes(in.Name),
	}
	for path, body := range support {
		if err := writeFile(path, body); err != nil {
			return err
		}
	}
	// Apache's ServerRoot and nginx's temp paths live here, and neither
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
	// setenvif because Kirby's .htaccess uses SetEnvIf outside any
	// <IfModule>: without it every request is a 500 (webserver-install
	// .feature, "A Kirby project needs no webserver configuration").
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
# Your own directives go in config/vhosts/<project>.<webserver>.conf.`

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
  # A name no project has is refused here, rather than answered by whichever
  # project happens to come first.
  server {
    listen {{.HTTPPort}} default_server;
    listen [::]:{{.HTTPPort}} default_server;
    return 404;
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

// kirbyRules is the body every nginx server block shares. It is Kirby's own
// nginx recipe, written as the Starterkit's .htaccess states it for Apache,
// so a Kirby project needs no custom config under either webserver
// (webserver-install.feature, "A Kirby project needs no webserver
// configuration"): dot-files and the content, site and kirby folders are
// handed to Kirby, which answers with its error page, never with the file.
//
// TODO(generic-templates): these rules, and the content/site/kirby
// DirectoryMatch in apacheSite, are Kirby's and are applied to every project.
// Kirby was the inspiration, not the target: they move to the Kirby template
// once templates carry their own rewrite recipe (see project.Templates).
//
// Each server block logs to the project's own folder. PHP's warnings and
// errors arrive there too: PHP-FPM sends them to the webserver over FastCGI,
// which logs them for the server block that made the request
// (project-logs.feature).
const kirbyRules = `{{define "kirby"}}  root "{{.DocRoot}}";
  index index.php index.html;
  add_header X-Content-Type-Options nosniff;
  access_log "{{.LogDir}}/access.log";
  error_log "{{.LogDir}}/error.log";

  rewrite (^|/)\.(?!well-known/) /index.php last;
  rewrite ^/(content|site|kirby)/ /index.php last;

  location / {
    try_files $uri $uri/ /index.php$is_args$args;
  }

  location ~ \.php$ {
    try_files $uri =404;
    fastcgi_pass 127.0.0.1:{{.PHPPort}};
    fastcgi_index index.php;
    include "{{.Conf.FastCGIParams}}";
    fastcgi_param SCRIPT_FILENAME $document_root$fastcgi_script_name;
{{- if .Conf.Front}}
    fastcgi_param SERVER_PORT $server_port;
    fastcgi_param HTTPS $https if_not_empty;
{{- else}}
    fastcgi_param SERVER_PORT $wharf_port;
    fastcgi_param HTTPS $wharf_https if_not_empty;
{{- end}}
  }
{{if .Include}}
  include "{{.Include}}";
{{end}}{{end}}`

var nginxVhostTemplate = template.Must(template.New("nginx-vhost").Parse(kirbyRules + generatedHeader + `
# {{.Name}}
{{if .Conf.Front}}
server {
  listen {{.Conf.HTTPPort}};
  listen [::]:{{.Conf.HTTPPort}};
  server_name {{.Hostname}};
{{template "kirby" .}}}
{{if .SSL}}
server {
  listen {{.Conf.HTTPSPort}} ssl;
  listen [::]:{{.Conf.HTTPSPort}} ssl;
  server_name {{.Hostname}};
  ssl_certificate "{{.CertFile}}";
  ssl_certificate_key "{{.KeyFile}}";
{{template "kirby" .}}}
{{end}}{{else}}
# Behind Wharf's front door, on loopback only.
server {
  listen 127.0.0.1:{{.Port}};
  server_name {{.Hostname}};
{{template "kirby" .}}}
{{end}}`))

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
{{if .Front}}
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

const apacheSite = `{{define "site"}}  DocumentRoot "{{.DocRoot}}"
  ErrorLog "{{.LogDir}}/error.log"
  CustomLog "{{.LogDir}}/access.log" common

  <Directory "{{.DocRoot}}">
    Options Indexes FollowSymLinks
    AllowOverride All
    Require all granted
  </Directory>

  # Apache appends the script's path to the handler URL. A Windows path starts
  # with its drive letter, which would run into the port without the slash;
  # the rule after it hands PHP the path as the OS spells it, "C:/..." on
  # Windows and "/..." elsewhere (webserver-install.feature, "A Kirby project
  # needs no webserver configuration").
  <FilesMatch \.php$>
    SetHandler "proxy:fcgi://127.0.0.1:{{.PHPPort}}/"
  </FilesMatch>
  ProxyFCGISetEnvIf "reqenv('SCRIPT_FILENAME') =~ m|^proxy:fcgi://[^/]+/([A-Za-z]:)?(/.*)|" SCRIPT_FILENAME "$1$2"

  <DirectoryMatch "^{{.DocRootRe}}/(content|site|kirby)/">
    Require all denied
  </DirectoryMatch>
{{if .Include}}
  Include "{{.Include}}"
{{end}}{{end}}`

var apacheVhostTemplate = template.Must(template.New("apache-vhost").Parse(apacheSite + generatedHeader + `
# {{.Name}}
{{if .Conf.Front}}
<VirtualHost *:{{.Conf.HTTPPort}}>
  ServerName {{.Hostname}}
{{template "site" .}}</VirtualHost>
{{if .SSL}}
<VirtualHost *:{{.Conf.HTTPSPort}}>
  ServerName {{.Hostname}}
  SSLEngine on
  SSLCertificateFile "{{.CertFile}}"
  SSLCertificateKeyFile "{{.KeyFile}}"
{{template "site" .}}</VirtualHost>
{{end}}{{else}}
# Behind Wharf's front door, on loopback only. PHP is told the scheme and port
# the browser used, or Kirby writes this instance's own port into every link.
<VirtualHost 127.0.0.1:{{.Port}}>
  ServerName {{.Hostname}}
  ProxyFCGISetEnvIf "-n %{HTTP:X-Forwarded-Port}" SERVER_PORT "%{HTTP:X-Forwarded-Port}"
  ProxyFCGISetEnvIf "%{HTTP:X-Forwarded-Proto} == 'https'" HTTPS "on"
{{template "site" .}}</VirtualHost>
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
