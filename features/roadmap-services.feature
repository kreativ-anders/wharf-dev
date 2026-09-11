@roadmap
Feature: Roadmap services (not part of v1)
  These scenarios exist to fix intended behaviour ahead of time. None of
  them are implemented in v1, which is PHP-only (see dev/architecture.md
  §2, dev/design-principles.md §1). No task should implement these without
  first revisiting the v1 scope decision.

  Scenario: Database service toggle (MySQL / PostgreSQL)
    Given "mysql" and "postgres" are registered as available database services
    When the user activates "postgres" as the active database
    Then the daemon stops "mysql" if running
    And starts "postgres" using the same start/stop mechanism as webserver toggling

  Scenario: Mailpit as a managed service
    Given "mailpit" is registered as a mail-catcher service
    When the user enables "mailpit" in Settings
    Then the daemon starts the Mailpit binary as a managed subprocess
    And outgoing mail from PHP projects is captured and viewable via Mailpit's own UI

  Scenario: Additional runtimes (Node.js, Go, Python)
    Given a runtime "node" version "20.x" is registered
    When a project's config specifies "runtime: node"
    Then the daemon serves that project using the Node runtime instead of PHP
    And version switching follows the same folder/symlink pattern as PHP versions

  Scenario: Enabling a roadmap service surfaces it in Settings
    Given a roadmap service has been implemented and flagged "stable"
    When the corresponding Settings section is un-hidden
    Then it appears in the same location described in settings.feature's
      "Settings screen hides roadmap services in v1" scenario, now un-hidden
