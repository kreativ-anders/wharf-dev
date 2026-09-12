@v1
Feature: Per-project logs
  As a developer
  I want each project to keep its own logs
  So that I can find what went wrong with one project without reading everyone's

  Every project gets a folder of its own under "data/log/projects/". The
  webserver serving it writes that project's requests and errors there. PHP's
  warnings and errors reach the same error log: PHP hands them to the
  webserver with the response, and the webserver logs them for the project
  the request was for.

  Background:
    Given the daemon is running
    And projects "my-kirby-site" and "other-site" exist

  Scenario: Each project writes its own logs
    Given both projects are served by the global nginx
    When they are started
    Then "my-kirby-site" logs requests and errors to "data/log/projects/my-kirby-site/"
    And "other-site" logs to its own folder, not to "my-kirby-site"'s

  Scenario: A project keeps its logs on either webserver
    Given "my-kirby-site" is served by apache behind the nginx front door
    When it is started
    Then its apache writes to "data/log/projects/my-kirby-site/" as well

  Scenario: Opening a project's logs
    When the user opens the settings of "my-kirby-site"
    Then "Logs" offers to open "data/log/projects/my-kirby-site/" in the file manager
