@v1
Feature: Pretty URLs under .localhost
  As a developer
  I want each project reachable under a friendly local domain
  So that I don't need to remember ports

  Every project is published as "<project>.localhost". The name is reserved
  for the loopback address (RFC 6761): macOS, Linux and every current browser
  answer it themselves, so nothing is written to the hosts file and nothing
  asks for a password. A made-up TLD such as ".wharf" or ".test" depends on
  the hosts file instead, which the macOS 26 resolver does not reliably read.

  Background:
    Given the daemon is running

  Scenario: Every project is reachable under its own .localhost name
    Given a new folder "my-kirby-site" is added under "www/"
    When the user adds "my-kirby-site" as a project
    Then it is reachable at "http://my-kirby-site.localhost"
    And no port is part of that URL, whichever webserver serves it

  Scenario: Adding a project never asks for a password
    When the user adds or scaffolds a project
    Then the hosts file is not written
    And no elevation prompt is shown

  Scenario: The webserver answers over IPv6 as well as IPv4
    Given "my-kirby-site" is running
    Then the webserver listens on port 80 on both 127.0.0.1 and ::1
    And Safari, which resolves "my-kirby-site.localhost" to ::1 first, reaches it

  Scenario: Removing a project added under the old domain removes its hosts entry
    Given "my-kirby-site" was added when projects were published as "my-kirby-site.wharf"
    And its hosts entry "127.0.0.1 my-kirby-site.wharf" still exists
    When the user removes the project
    Then that hosts entry is deleted through one elevation prompt
    And on macOS, the same prompt restarts the system resolver so it re-reads the hosts file
    And no other line of the hosts file is affected
