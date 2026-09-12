@v1
Feature: Local SSL certificates
  As a developer
  I want HTTPS to work with one switch
  So that I never have to install or configure mkcert myself

  Certificates come from mkcert's local certificate authority. Browsers only
  accept them once that authority is trusted, which needs administrator rights
  once per machine.

  Background:
    Given the daemon is running
    And project "my-kirby-site" exists with "ssl: false"

  Scenario: Enabling SSL when mkcert is not installed
    Given mkcert is neither in "bin/mkcert/" nor on the PATH
    When the user enables SSL for "my-kirby-site"
    Then mkcert is downloaded into "bin/mkcert/" and its checksum verified
    And a certificate for "my-kirby-site.localhost" is issued

  Scenario: Trusting the local certificate authority
    Given the local certificate authority is not trusted yet
    When the user enables SSL for a project for the first time
    Then an elevation prompt asks to trust the local certificate authority
    And, once approved, browsers accept the project's certificate

  Scenario: Trust is declined
    Given the local certificate authority is not trusted yet
    When the user enables SSL for "my-kirby-site"
    And the elevation prompt to trust the certificate authority is declined
    Then SSL is still enabled for "my-kirby-site"
    And the GUI says browsers will warn until the authority is trusted
    And Settings offers to ask again

  Scenario: mkcert cannot be downloaded
    Given mkcert is not installed and the machine has no internet connection
    When the user enables SSL for "my-kirby-site"
    Then SSL stays off for "my-kirby-site"
    And the GUI reports that mkcert could not be fetched

  Scenario: HTTPS for a project on the other webserver ends at the front door
    Given the global active webserver is "nginx"
    And "legacy-app" has "webserver_override: apache"
    When the user enables SSL for "legacy-app"
    Then the global nginx serves "legacy-app.localhost" on port 443 with its certificate
    And forwards each request to "legacy-app"'s own apache, which tells PHP it was HTTPS on port 443
    And its URL is "https://legacy-app.localhost"
