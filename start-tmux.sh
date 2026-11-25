#!/bin/bash

# Ensure the 'bandcampserver' session exists
if ! tmux has-session -t bandcampserver 2>/dev/null; then
  # Create a new session named 'bandcampserver', detached
  cd /workspaces/bandcampserver
  tmux new-session -d -s bandcampserver
fi
