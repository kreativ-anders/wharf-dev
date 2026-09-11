@v1
Feature: One application
  As a developer
  I want to launch one application and have it work
  So that I never have to think about a daemon at all

  The daemon is an implementation detail (dev/architecture.md §3). Nothing in
  the user's experience should reveal that there are two processes: no second
  thing to start, no order to get right, no leftover process after quitting.

  Scenario: Launching the application with nothing running
    Given no Wharf daemon is running
    When the user launches the application
    Then the application starts the daemon itself
    And the project list appears without any further action from the user

  Scenario: Launching the application when a daemon is already running
    Given a Wharf daemon is already running for the same root folder
    When the user launches the application
    Then it connects to the running daemon
    And no second daemon is started

  Scenario: Quitting an application that started its own daemon
    When the user quits the application
    Then the daemon it started is stopped
    And no managed service is left running

  Scenario: Quitting an application that attached to an existing daemon
    Given the daemon was already running before the application started
    When the user quits the application
    Then that daemon keeps running
    And the projects it serves stay up

  Scenario: The application exits without shutting its daemon down
    Given the application started the daemon itself
    When the application exits unexpectedly
    Then the daemon notices that its parent is gone
    And it stops itself rather than being left behind

  Scenario: The daemon binary cannot be found
    Given the wharfd binary is not where the application expects it
    When the user launches the application
    Then the application says where it looked and how to build it
    And it keeps retrying, so building it is enough to recover
