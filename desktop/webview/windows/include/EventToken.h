// WebView2's event token ABI. Provide the SDK's case-sensitive include on
// Linux, including older MinGW distributions without eventtoken.h.
#ifndef __eventtoken_h__
#define __eventtoken_h__
#include <windows.h>
typedef struct EventRegistrationToken {
    INT64 value;
} EventRegistrationToken;
#endif
