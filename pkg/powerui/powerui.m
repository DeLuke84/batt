#import <Foundation/Foundation.h>
#import <dlfcn.h>
#import <objc/message.h>
#import <objc/runtime.h>
#include <string.h>

#include "powerui.h"

static const char *kPowerUIPath = "/System/Library/PrivateFrameworks/PowerUI.framework/Versions/A/PowerUI";

// Method type encodings observed on macOS 27.0 (26A428):
//   initWithClientName:              @24@0:8@16
//   isMCLSupported                   B16@0:8
//   getMCLLimitWithError:            C24@0:8^@16   (unsigned char)
//   setMCLLimit:error:               B28@0:8C16^@20
//   isMCLCurrentlyEnabled:           Q24@0:8^@16
//   availableChargeLimitsWithError:  @24@0:8^@16   (NSArray<NSNumber *>)
//   disableMCL:                      B24@0:8^@16
typedef id (*initWithClientNameFn)(id, SEL, id);
typedef BOOL (*boolFn)(id, SEL);
typedef unsigned char (*ucharErrFn)(id, SEL, NSError **);
typedef BOOL (*boolUcharErrFn)(id, SEL, unsigned char, NSError **);
typedef unsigned long long (*ullErrFn)(id, SEL, NSError **);
typedef id (*idErrFn)(id, SEL, NSError **);
typedef BOOL (*boolErrFn)(id, SEL, NSError **);

// The client is created once and kept for the lifetime of the process. The Go
// side serializes all calls, so no locking is needed here.
static id gClient = nil;
static BOOL gClientProbed = NO;

static void setError(char **err, NSString *message) {
    if (err == NULL) {
        return;
    }
    *err = strdup(message != nil ? [message UTF8String] : "unknown PowerUI error");
}

static void setNSError(char **err, NSError *error, NSString *fallback) {
    setError(err, error != nil ? [error description] : fallback);
}

static BOOL responds(id obj, const char *sel) {
    return obj != nil && [obj respondsToSelector:sel_registerName(sel)];
}

static id client(void) {
    if (gClientProbed) {
        return gClient;
    }
    gClientProbed = YES;

    if (dlopen(kPowerUIPath, RTLD_NOW) == NULL) {
        return nil;
    }
    Class cls = NSClassFromString(@"PowerUISmartChargeClient");
    if (cls == Nil) {
        return nil;
    }
    id instance = [cls alloc];
    if (!responds(instance, "initWithClientName:")) {
        [instance release];
        return nil;
    }
    instance = ((initWithClientNameFn)objc_msgSend)(instance, sel_registerName("initWithClientName:"), @"batt");
    if (!responds(instance, "isMCLSupported") ||
        !responds(instance, "getMCLLimitWithError:") ||
        !responds(instance, "setMCLLimit:error:") ||
        !responds(instance, "isMCLCurrentlyEnabled:") ||
        !responds(instance, "availableChargeLimitsWithError:") ||
        !responds(instance, "disableMCL:")) {
        [instance release];
        return nil;
    }
    gClient = instance;
    return gClient;
}

int batt_powerui_supported(void) {
    @autoreleasepool {
        id c = client();
        if (c == nil) {
            return 0;
        }
        return ((boolFn)objc_msgSend)(c, sel_registerName("isMCLSupported")) ? 1 : 0;
    }
}

int batt_powerui_available_limits(int *limits, int max, int *count, char **err) {
    @autoreleasepool {
        id c = client();
        if (c == nil) {
            setError(err, @"PowerUI smart charge client is unavailable");
            return -1;
        }
        NSError *error = nil;
        id result = ((idErrFn)objc_msgSend)(c, sel_registerName("availableChargeLimitsWithError:"), &error);
        if (result == nil || ![result isKindOfClass:[NSArray class]]) {
            setNSError(err, error, @"availableChargeLimitsWithError: returned no limits");
            return -1;
        }
        int n = 0;
        for (id value in (NSArray *)result) {
            if (n >= max) {
                break;
            }
            if ([value respondsToSelector:@selector(intValue)]) {
                limits[n++] = [value intValue];
            }
        }
        *count = n;
        return 0;
    }
}

int batt_powerui_get_limit(int *limit, int *enabled, char **err) {
    @autoreleasepool {
        id c = client();
        if (c == nil) {
            setError(err, @"PowerUI smart charge client is unavailable");
            return -1;
        }
        NSError *error = nil;
        unsigned char value = ((ucharErrFn)objc_msgSend)(c, sel_registerName("getMCLLimitWithError:"), &error);
        if (error != nil) {
            setNSError(err, error, nil);
            return -1;
        }
        error = nil;
        unsigned long long on = ((ullErrFn)objc_msgSend)(c, sel_registerName("isMCLCurrentlyEnabled:"), &error);
        if (error != nil) {
            setNSError(err, error, nil);
            return -1;
        }
        *limit = (int)value;
        *enabled = on != 0 ? 1 : 0;
        return 0;
    }
}

int batt_powerui_set_limit(int limit, char **err) {
    @autoreleasepool {
        id c = client();
        if (c == nil) {
            setError(err, @"PowerUI smart charge client is unavailable");
            return -1;
        }
        NSError *error = nil;
        BOOL ok = ((boolUcharErrFn)objc_msgSend)(c, sel_registerName("setMCLLimit:error:"), (unsigned char)limit, &error);
        if (!ok) {
            setNSError(err, error, @"setMCLLimit:error: failed");
            return -1;
        }
        return 0;
    }
}

int batt_powerui_disable(char **err) {
    @autoreleasepool {
        id c = client();
        if (c == nil) {
            setError(err, @"PowerUI smart charge client is unavailable");
            return -1;
        }
        NSError *error = nil;
        BOOL ok = ((boolErrFn)objc_msgSend)(c, sel_registerName("disableMCL:"), &error);
        if (!ok) {
            setNSError(err, error, @"disableMCL: failed");
            return -1;
        }
        return 0;
    }
}
