// Command ft-msix-stub is the launchable "app" of the sparse MSIX package that
// registers the Explorer context-menu handler. Windows requires the package to
// declare an Application with an Executable; pointing that at the real GUI app
// (ft-desktop.exe) makes the shell try to activate it while building the menu,
// which hangs Explorer. A no-op stub keeps activation trivial.
package main

func main() {}
