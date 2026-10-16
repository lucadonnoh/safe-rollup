package logcli_test

// TestSetGlobalLogHandlerTagsRecords checks that records logged through the
// global logger, and through its go-ethereum view, carry the global=true tag
// that SetGlobalLogHandler puts in the logger's default context.
