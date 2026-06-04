#!/bin/bash
# Simple test script for bash debugging fixture
# Has variables and a function call for stepping through

greeting="Hello from bash debugging"

say_hello() {
	local name="$1"
	local message="$greeting, $name"
	echo "$message"
}

name="World"
say_hello "$name"

count=0
while [ "$count" -lt 3 ]; do
	count=$((count + 1))
done

echo "Done."
