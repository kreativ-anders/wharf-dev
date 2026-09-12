// Package proctree keeps a managed process together with every process it
// starts, so that stopping a service stops all of it.
//
// nginx, Apache and php-fpm are a master with workers, and every worker holds
// the listening socket. Kill only the master and the workers keep the port,
// so every later start fails with "port 80 still in use"
// (service-management.feature, "Stopping a webserver stops its worker
// processes too"). The two adapters behind one API:
//
//   - macOS and Linux: the process leads its own process group, and a kill
//     reaches the whole group.
//   - Windows: there are no process groups to signal and no parent-child kill,
//     so the process runs in its own job object. Terminating the job ends every
//     process in it, and the job is set to kill on close: when the daemon dies,
//     however it dies, Windows closes the handle and the services die with it.
//
// This package and internal/elevate are the only platform-specific code in the
// daemon (dev/architecture.md §4).
package proctree
