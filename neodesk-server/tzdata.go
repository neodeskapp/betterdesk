package main

// The server reads BILLING_TIMEZONE with time.LoadLocation. The zone
// database built into the binary answers on hosts that have none, such as
// Windows without a Go installation.
import _ "time/tzdata"
