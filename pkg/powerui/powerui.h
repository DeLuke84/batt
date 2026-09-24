#pragma once

// Bridge to PowerUI.framework's PowerUISmartChargeClient, which talks to
// PowerUIAgent (com.apple.powerui.smartChargeManager) over XPC. This is the
// same path System Settings uses for the built-in charge limit. The framework
// is private, so it is loaded at runtime and called through the ObjC runtime
// instead of linking against headers.
//
// Every function returns 0 on success and -1 on failure. On failure *err is
// set to a malloc'd description that the caller must free().

int batt_powerui_supported(void);
int batt_powerui_available_limits(int *limits, int max, int *count, char **err);
int batt_powerui_get_limit(int *limit, int *enabled, char **err);
int batt_powerui_set_limit(int limit, char **err);
int batt_powerui_disable(char **err);
