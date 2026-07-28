---
name: klaren-llm-session-review
description: When being a Karen is helpful; complain about how an LLM is performing poorly by examining its session log. The complains should cover everything, from the harness loop, decisions made, the tools/CLIs/APIs chosen, etc. The complain helps people to improve the LLM experience by fixing things the LLM relies on.
allowed-tools: Bash, Read, Edit, Write, Glob, Grep
---

## Important

* The tool name is 'klaren', NOT 'karen'. Do not attempt to correct the spelling to 'karen'.

## Requirements

* An LLM session log file. If not FAIL HARD and LOUD. Do NOT contiue.
  * The goal the LLM was trying to achieve should be the user prompt in the session log.
    * If the goal was unclear, FAIL HARD and LOUD. Do NOT coninue.

## Goal

Your job is to analyze a LLM session and complain about what the tools/CLIs/APIs called, their descriptions/documentation, input options/arguments, and output data (the format, the actual content or lack of it, etc.), and enumerate what could have otherwise made your task/goal easier, less time/token consuming, e.g., we should  modify the tools/CLIs/APIs (input, output, etc.), and their descriptions/documentations, input options/arguments, and outputs, e.g., by adding more/better option/arguments to retrieve more specific data, return more data, introduce another tool/CLI/API endpoint, a better harness/loop of the agent, e.g., writing a code instead to run once isntead of multiple tool calls,  etc.

Example (not exhaustive) of things to look for:
* High repetitive activity could indicate issues
* High frequency activity could poor choice of polling period
* Cyclic calls of same tools, could indicate the availability of a consolidated API endpoint could be better
* Use of tools, APIs, CLIs, whose output did not yield expected results, or the results were not used
* Use of tools, APIs, CLIs, whose output overlaps significantly
* The lack of parameters/filters which could have reduced superfluous output
* Un-ncessary steps/detours that could have been avoided

## Output

* Summary of stesp of plan the LLM took
* Summary of tools, APIS, CLIs, etc, the LLM used to fullfil each step of the plan
* Summary of the issues, tool/API/CLI, etc.., fixes, quantify wastage (by time/tokens), severities
