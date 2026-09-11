package runtime

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"text/template"

	"github.com/manuel-steinberg/wharf/daemon/internal/config"
	"github.com/manuel-steinberg/wharf/daemon/internal/hostsfile"
	"github.com/manuel-steinberg/wharf/daemon/internal/layout"
	"github.com/manuel-steinberg/wharf/daemon/internal/webserver"
)

// vhost is one project as the config templates see it.
type vhost struct {
	Name     string
	Hostname string
	DocRoot  string
	// DocRootRe is DocRoot escaped for Apache's regex-matched sections: a
	// folder added from elsewhere can contain any character.
	DocRootRe string
	// Include is the project's custom directives for this webserver, or ""
	// when it has none (app-configuration.feature).
	Include  string
	SSL      bool
	CertFile string
	KeyFile  string
	PHPPort  int
	// Port is the project's own raw port. The vhost listens on it as well as
	// on the shared HTTP port, so the project stays reachable when no hosts
	// entry exists (pretty-urls.feature, "Elevation is declined").
	Port int
	// Conf is the instance the vhost belongs to, for its ports and paths.
	Conf *confData
}

type module struct{ Name, Path string }

type confData struct {
	ListenPort int
	HTTPSPort  int
	LogDir     string
	// RunDir holds this instance's pid file and scratch files. The global
	// webserver and a project's own instance run side by side, so each needs
	// its own: Apache refuses to start over a pid file whose process lives.
	RunDir        string
	MimeTypes     string
	FastCGIParams string
	Modules       []module
	// Includes are the per-project vhost files the main config pulls in
	// (webserver-install.feature, "One webserver, one config file per
	// project").
	Includes []string
	Vhosts   []vhost
	AnySSL   bool
}

// rendered is a webserver instance's configuration: its main file and one
// file per project.
type rendered struct {
	Main   string
	RunDir string
	Vhosts map[string]string // path → content
}

// VhostPath is where a project's generated server block for one webserver
// lives. Only one instance serves a project at a time, so one file suffices.
func (r *Resolver) VhostPath(server, project string) string {
	return filepath.Join(r.genDir(), server, project+".conf")
}

// renderWebserverConf generates an instance's configuration: one vhost file
// per project, each with its document root and PHP routed to the FastCGI
// backend for that project's resolved PHP version, and a main file that
// includes them.
func (r *Resolver) renderWebserverConf(cfg *config.Config, in webserver.Install, instance string, projects []config.Project, listenPort, httpsPort int) (rendered, error) {
	data := &confData{
		ListenPort:    listenPort,
		HTTPSPort:     httpsPort,
		LogDir:        forwardSlash(r.Root.LogDir()),
		RunDir:        forwardSlash(filepath.Join(r.genDir(), "run", instance)),
		FastCGIParams: forwardSlash(filepath.Join(r.genDir(), "fastcgi_params")),
		MimeTypes:     forwardSlash(filepath.Join(r.genDir(), in.Name+"-mime.types")),
	}
	for _, p := range projects {
		docRoot := forwardSlash(ProjectDir(r.Root, p))
		v := vhost{
			Name:      p.Name,
			Hostname:  hostsfile.Hostname(p.Name),
			DocRoot:   docRoot,
			DocRootRe: regexp.QuoteMeta(docRoot),
			SSL:       p.SSL,
			CertFile:  forwardSlash(CertPath(r.Root, p.Name)),
			KeyFile:   forwardSlash(KeyPath(r.Root, p.Name)),
			PHPPort:   PHPPort(cfg, cfg.PHPVersionFor(p)),
			Port:      p.Port,
			Conf:      data,
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

	var main, host *template.Template
	switch in.Name {
	case webserver.Nginx:
		main, host = nginxTemplate, nginxVhostTemplate
	case webserver.Apache:
		main, host = apacheTemplate, apacheVhostTemplate
		data.Modules = apacheModules(in.Modules, data.AnySSL)
	default:
		return rendered{}, fmt.Errorf("unknown webserver %q", in.Name)
	}

	out := rendered{Vhosts: map[string]string{}, RunDir: filepath.FromSlash(data.RunDir)}
	for _, v := range data.Vhosts {
		var sb strings.Builder
		if err := host.Execute(&sb, v); err != nil {
			return rendered{}, err
		}
		path := r.VhostPath(in.Name, v.Name)
		out.Vhosts[path] = sb.String()
		data.Includes = append(data.Includes, forwardSlash(path))
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
	if err := os.MkdirAll(conf.RunDir, 0o755); err != nil {
		return err
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
	for _, m := range []string{"authz_core", "unixd", "dir", "mime", "log_config", "rewrite", "headers", "proxy", "proxy_fcgi"} {
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
	return filepath.Join(root.CertDir(), hostsfile.Hostname(project)+".pem")
}

func KeyPath(root layout.Root, project string) string {
	return filepath.Join(root.CertDir(), hostsfile.Hostname(project)+"-key.pem")
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

  # One file per project.
{{range .Includes}}  include "{{.}}";
{{end}}}
`))

var nginxVhostTemplate = template.Must(template.New("nginx-vhost").Parse(generatedHeader + `
# {{.Name}}

server {
  listen {{.Conf.ListenPort}};{{if and .Port (ne .Port .Conf.ListenPort)}}
  listen {{.Port}};{{end}}
  server_name {{.Hostname}};
  root "{{.DocRoot}}";
  index index.php index.html;

  location / {
    try_files $uri $uri/ /index.php?$query_string;
  }

  location ~ \.php$ {
    fastcgi_pass 127.0.0.1:{{.PHPPort}};
    fastcgi_index index.php;
    fastcgi_param SCRIPT_FILENAME $document_root$fastcgi_script_name;
    include "{{.Conf.FastCGIParams}}";
  }

  # Kirby keeps content, site and kirby folders out of the web root's reach.
  location ~ ^/(content|site|kirby)/ { deny all; }
{{if .Include}}
  include "{{.Include}}";
{{end}}}
{{if .SSL}}
server {
  listen {{.Conf.HTTPSPort}} ssl;
  server_name {{.Hostname}};
  root "{{.DocRoot}}";
  index index.php index.html;

  ssl_certificate "{{.CertFile}}";
  ssl_certificate_key "{{.KeyFile}}";

  location / {
    try_files $uri $uri/ /index.php?$query_string;
  }

  location ~ \.php$ {
    fastcgi_pass 127.0.0.1:{{.PHPPort}};
    fastcgi_index index.php;
    fastcgi_param SCRIPT_FILENAME $document_root$fastcgi_script_name;
    fastcgi_param HTTPS on;
    include "{{.Conf.FastCGIParams}}";
  }

  location ~ ^/(content|site|kirby)/ { deny all; }
{{if .Include}}
  include "{{.Include}}";
{{end}}}
{{end}}`))

var apacheTemplate = template.Must(template.New("apache").Parse(generatedHeader + `
ServerRoot "{{.RunDir}}"
DefaultRuntimeDir "{{.RunDir}}"
PidFile "{{.RunDir}}/httpd.pid"
ServerName localhost
Listen {{.ListenPort}}
{{range .Vhosts}}{{if and .Port (ne .Port $.ListenPort)}}Listen {{.Port}}
{{end}}{{end}}{{if .AnySSL}}Listen {{.HTTPSPort}}
{{end}}
{{range .Modules}}LoadModule {{.Name}} "{{.Path}}"
{{end}}
TypesConfig "{{.MimeTypes}}"
ErrorLog "{{.LogDir}}/apache-error.log"
LogFormat "%h %l %u %t \"%r\" %>s %b" common
CustomLog "{{.LogDir}}/apache-access.log" common
DirectoryIndex index.php index.html

# One file per project.
{{range .Includes}}Include "{{.}}"
{{end}}`))

var apacheVhostTemplate = template.Must(template.New("apache-vhost").Parse(generatedHeader + `
# {{.Name}}

<VirtualHost *:{{.Conf.ListenPort}}{{if and .Port (ne .Port .Conf.ListenPort)}} *:{{.Port}}{{end}}>
  ServerName {{.Hostname}}
  DocumentRoot "{{.DocRoot}}"

  <Directory "{{.DocRoot}}">
    Options Indexes FollowSymLinks
    AllowOverride All
    Require all granted
  </Directory>

  <FilesMatch \.php$>
    SetHandler "proxy:fcgi://127.0.0.1:{{.PHPPort}}"
  </FilesMatch>

  <DirectoryMatch "^{{.DocRootRe}}/(content|site|kirby)/">
    Require all denied
  </DirectoryMatch>
{{if .Include}}
  Include "{{.Include}}"
{{end}}</VirtualHost>
{{if .SSL}}
<VirtualHost *:{{.Conf.HTTPSPort}}>
  ServerName {{.Hostname}}
  DocumentRoot "{{.DocRoot}}"

  SSLEngine on
  SSLCertificateFile "{{.CertFile}}"
  SSLCertificateKeyFile "{{.KeyFile}}"

  <Directory "{{.DocRoot}}">
    Options Indexes FollowSymLinks
    AllowOverride All
    Require all granted
  </Directory>

  <FilesMatch \.php$>
    SetHandler "proxy:fcgi://127.0.0.1:{{.PHPPort}}"
  </FilesMatch>
{{if .Include}}
  Include "{{.Include}}"
{{end}}</VirtualHost>
{{end}}`))
