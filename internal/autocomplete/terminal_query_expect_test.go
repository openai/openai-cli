package autocomplete

import "strings"

// Match markers and device queries in wire order. A separate expect_before or
// expect_after handler can discard an earlier token from the same buffered read.
func terminalQueryExpect(driver string) string {
	const waits = `
proc terminal_expect {kind {needle ""}} {
    global spawn_id timeout
    set queries "\033\\\[0c|\033\\\[6n"
    switch -- $kind {
        -exact {
            regsub -all {[][\\.^$*+?(){}|]} $needle {\\&} marker
            set pattern "(?:$marker)|(?:$queries)"
        }
        -re {set pattern "(?:$needle)|(?:$queries)"}
        eof {set pattern $queries}
        default {error "unsupported terminal wait: $kind"}
    }
    expect {
        -re $pattern {
            switch -- $expect_out(0,string) {
                "\033\1330c" {send -- "\033\133?1;2c"; exp_continue}
                "\033\1336n" {send -- "\033\1331;1R"; exp_continue}
                default {return}
            }
        }
        eof {
            if {$kind eq "eof"} {return}
            error "shell exited before terminal marker: $needle"
        }
    }
}

`
	return waits + strings.NewReplacer(
		"expect -exact ", "terminal_expect -exact ",
		"expect -re ", "terminal_expect -re ",
		"expect eof", "terminal_expect eof",
	).Replace(driver)
}
