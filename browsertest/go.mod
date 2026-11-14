module github.com/go-widgets/webcanvas/browsertest

go 1.27.3

replace github.com/go-widgets/webcanvas => ../

require (
	github.com/chromedp/cdproto v0.160.2
	github.com/chromedp/chromedp v0.23.1
	github.com/go-widgets/textinput v0.1.1
	github.com/go-widgets/webcanvas v0.14.3
)

require github.com/go-json-experiment/json v0.0.0-20260213210345-44df1a37e875 // indirect
