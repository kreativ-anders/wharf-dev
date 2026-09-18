@v1
Feature: PHP in the terminal
  As a developer
  I want my terminal and my editor to run the PHP that serves my projects
  So that Composer, the Kirby CLI and my editor use the same version without a path set by hand

  Wharf keeps one folder, "bin/path", whose "php" runs the global default
  version: a link on macOS and Linux, a "php.cmd" on Windows. Putting that
  folder on PATH is on from the first start, so "php" in a new terminal is the
  PHP Wharf serves with; the user can turn it off. It asks for no password on
  any OS: the shell startup files and the Windows user environment belong to
  the user.

  Scenario: Use in terminal is on from the first start
    When Wharf starts for the first time
    Then "terminal": true is written to config/wharf.json
    And "bin/path" is put on the terminal PATH, as turning "Use in terminal" on does
    And a Wharf folder started only for tests leaves it off

  Scenario: The global default PHP is kept in one folder
    Given PHP "8.3" and "8.4" are installed and "8.4" is the global default
    Then the "php" in "bin/path" runs PHP "8.4"
    When the user selects "8.3" in the PHP runtime picker
    Then the "php" in "bin/path" runs PHP "8.3"

  Scenario: The global default PHP is not installed
    Given the global default PHP version is not installed
    Then "bin/path" holds no "php"
    And the PHP page says the terminal finds no PHP from Wharf until that version is installed

  Scenario: Wharf's own folder is not adopted as another PHP
    Given "bin/path" is on PATH
    When Wharf scans the machine for PHP
    Then "bin/path" is not offered as a PHP installation of its own

  Scenario: Putting Wharf's PHP on the terminal PATH
    Given the user's shell is zsh
    When the user turns on "Use in terminal" on the PHP page in Settings
    Then "terminal": true is written to config/wharf.json
    And a block marked as Wharf's, putting "bin/path" first on PATH, is added to the end of "~/.zshrc"
    And the same block is added to every other shell startup file that exists: bash's, "~/.profile" and fish's
    And on Windows "bin/path" is put first on the user's PATH instead
    And the PHP page names every place it changed and says a new terminal picks it up
    And no password is asked for

  Scenario: Turning it on again adds nothing twice
    Given "Use in terminal" is on
    When Wharf starts again, or the user turns it on again
    Then every startup file holds Wharf's block exactly once

  Scenario: Taking Wharf's PHP off the terminal PATH
    Given "Use in terminal" is on
    When the user turns it off
    Then Wharf's block is removed from every startup file, and on Windows "bin/path" from the user's PATH
    And every other line of those files is left as it was
    And the "terminal" key is removed from config/wharf.json

  Scenario: Only the last Wharf folder switched on is on the terminal PATH
    Given "Use in terminal" is on in one Wharf folder
    When the user turns it on in another Wharf folder
    Then a new terminal runs PHP from the other folder's "bin/path"
    And every startup file holds one Wharf block, naming the other folder
    When the user turns it off in the first Wharf folder
    Then the other folder's block is left as it was

  Scenario: A Wharf block broken by hand is not guessed at
    Given the user deleted the end line of Wharf's block in "~/.zshrc"
    When "Use in terminal" is turned on or off
    Then no startup file is changed
    And the message names the file and the line to delete or put back

  Scenario: Quitting Wharf leaves PHP in the terminal
    Given "Use in terminal" is on
    When the user chooses "Cast off"
    Then the startup files keep Wharf's block
    And a new terminal still runs PHP from "bin/path"

  Scenario: Resetting Wharf leaves PHP in the terminal, as a first start does
    Given "Use in terminal" is off
    When the user resets Wharf
    Then "Use in terminal" is on again, as after a first start
    And "bin/path" holds the "php" of the global default a first start selects
