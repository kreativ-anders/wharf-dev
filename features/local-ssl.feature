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
    And a certificate for "my-kirby-site.wharf" is issued

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

  Scenario: A project on its own webserver instance gets its own HTTPS port
    Given "legacy-app" has "webserver_override: apache" and port 8081
    When the user enables SSL for "legacy-app"
    Then "legacy-app" is served over HTTPS on port 8444
    And its URL is "https://legacy-app.wharf:8444"
    And it does not compete with the global webserver for port 443
