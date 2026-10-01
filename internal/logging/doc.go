// Package logging writes xserial application events and raw local output.
//
// Device traffic does not belong here and remains on stdout. Application
// events use Warn and Error; prompts, help, and in-place progress use Logger.Raw
// without formatting.
package logging
