@v1
Feature: PHP settings
  As a developer
  I want to change PHP's own settings — memory_limit, upload sizes, display_errors
  So that a project's needs do not mean finding and editing each PHP's php.ini

  The settings are a php.ini of Wharf's own, in "config/", read after each
  PHP's own php.ini: its values win, and a PHP adopted from the machine keeps
  the extensions its own configuration loads. Like custom webserver configs,
  it is a file an editor highlights, not keys in wharf.json.

  Background:
    Given the daemon is running

  Scenario: Editing PHP settings
    When the user chooses "Edit php.ini" under "PHP" in Settings
    Then "config/php.ini" is created with a commented starting point and opened in the editor
    And every PHP version Wharf runs reads it after its own php.ini

  Scenario: Saving PHP settings applies them
    Given a project is running
    When the user saves a change to "config/php.ini"
    Then every running PHP backend is restarted with the change
    And no webserver is restarted
