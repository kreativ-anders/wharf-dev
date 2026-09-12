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

  Scenario: Stopping a webserver stops its worker processes too
    Given "nginx" is running as a master process with worker processes
    When the daemon stops "nginx"
    Then its worker processes stop with it
    And none of them is left holding port 80

  Scenario: Per-project override takes precedence over the global default
    Given the global active webserver is "nginx"
    And project "legacy-app" has "webserver_override: apache" in its config
    When "legacy-app" is started
    Then "legacy-app" is served by its own "apache" instance, listening only on loopback
    And the global "nginx" instance forwards "legacy-app.localhost" to it
    And "legacy-app" is reachable at "http://legacy-app.localhost", without a port
    And PHP in "legacy-app" sees port 80, so Kirby builds its links without a port

  Scenario: Choosing the active webserver for a project starts no second instance
    Given the global active webserver is "nginx"
    When the user sets "my-kirby-site" to "nginx"
    Then "my-kirby-site" is served by the global "nginx" instance
    And no second webserver process is started for it

  Scenario: The front door answers on port 80 without projects of its own
    Given every project is served by the other webserver
    When one of them is started
    Then the global webserver starts on port 80 and forwards to it
    And a request for a name no project has is refused, not answered by another project
