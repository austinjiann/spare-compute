# User applications

The macOS application is a presentation layer. It communicates only with the
local Go daemon and must not own scheduling, trust, transfer, or execution
state.

The buildable Swift Package and development instructions are in `macos/`.

`control-center/` is the heavier desktop settings app. It owns device sync,
allowed work, project sync, relay settings, and future AI planner configuration
so the menu bar can stay minimal.
