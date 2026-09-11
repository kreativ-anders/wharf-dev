@v1
Feature: Webserver service management
  As a developer
  I want to switch the active webserver between Apache and Nginx
  So that I can match whichever a given project needs

  Background:
    Given the daemon is running
    And the config file lists "apache" and "nginx" as available webservers

  Scenario: Switching the active webserver
    Given "nginx" is the currently active webserver
    When the user selects "apache" as the active webserver in the GUI
    Then the daemon stops the running "nginx" process
    And the daemon starts the "apache" process
    And the config file's "services.webserver.active" value is updated to "apache"

  Scenario: Port conflict on switch
    Given "nginx" is bound to port 80
    When the user activates "apache" while "nginx" has not yet released port 80
    Then the daemon waits for the port to be released before starting "apache"
    And the GUI shows a "switching webserver" status until the new process is confirmed running

  Scenario: Per-project override takes precedence over the global default
    Given the global active webserver is "nginx"
    And project "legacy-app" has "webserver_override: apache" in its config
    When "legacy-app" is started
    Then "legacy-app" is served by "apache" on its own port
    And the global "nginx" instance is unaffected
