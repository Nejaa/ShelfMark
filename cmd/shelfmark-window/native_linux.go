//go:build webview_helper

package main

/*
#cgo pkg-config: gtk+-3.0 webkit2gtk-4.0
#include <gtk/gtk.h>
#include <webkit2/webkit2.h>

static int can_create_window(void) { return gtk_init_check(NULL, NULL); }

static void configure_window(void *handle) {
    GtkWidget *view = gtk_bin_get_child(GTK_BIN(handle));
    if (WEBKIT_IS_WEB_VIEW(view)) {
        // The UI needs no accelerated canvas or video. Software rendering avoids
        // depending on a destination system's GPU driver stack.
        webkit_settings_set_hardware_acceleration_policy(
            webkit_web_view_get_settings(WEBKIT_WEB_VIEW(view)),
            WEBKIT_HARDWARE_ACCELERATION_POLICY_NEVER);
    }
}
*/
import "C"

import webview "github.com/webview/webview_go"

func canCreateWindow() bool {
	return C.can_create_window() != 0
}

func configureWindow(window webview.WebView) {
	C.configure_window(window.Window())
}
