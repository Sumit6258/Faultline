module github.com/Sumit6258/Faultline/web/interactive-lab/server

go 1.22

require github.com/Sumit6258/Faultline/labs/kafka-messaging v0.0.0

// The labs are independent modules that never import each other. This server
// is the one consumer that imports one, on purpose: its whole job is to run
// the real lab code behind a browser page, so it points at the lab on disk.
replace github.com/Sumit6258/Faultline/labs/kafka-messaging => ../../../labs/kafka-messaging
