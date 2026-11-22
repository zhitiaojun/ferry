// ftmenu.dll — Windows 11 "modern" Explorer context-menu verb for ft.
//
// Implements IExplorerCommand (single verb, no sub-commands) and an
// IClassFactory so the DLL can be registered as a packaged COM server
// through a sparse MSIX package (see deploy/msix/AppxManifest.xml).
//
// When invoked it collects the selected filesystem paths and launches
// ft-desktop.exe with them, exactly like "Send To".

#define WIN32_LEAN_AND_MEAN
#include <windows.h>
#include <shobjidl.h>
#include <shellapi.h>
#include <stdio.h>

// {B7F1D2C4-8A31-4C77-9D2E-3A5B7C1E9F10}
static const GUID CLSID_FTMenu = {
    0xB7F1D2C4, 0x8A31, 0x4C77, {0x9D, 0x2E, 0x3A, 0x5B, 0x7C, 0x1E, 0x9F, 0x10}};

static const wchar_t *MENU_TITLE = L"\u7528 ft \u53d1\u9001"; // 用 ft 发送
static const wchar_t *MENU_TIP = L"\u7528 ft \u628a\u6587\u4ef6\u53d1\u9001\u5230\u5df2\u914d\u5bf9\u8bbe\u5907";

typedef struct FTMenu FTMenu;
struct FTMenu {
    IExplorerCommandVtbl *lpVtbl;
    LONG ref;
};

static HRESULT STDMETHODCALLTYPE FT_QueryInterface(IExplorerCommand *iface, REFIID riid, void **ppv);
static ULONG STDMETHODCALLTYPE FT_AddRef(IExplorerCommand *iface);
static ULONG STDMETHODCALLTYPE FT_Release(IExplorerCommand *iface);
static HRESULT STDMETHODCALLTYPE FT_GetTitle(IExplorerCommand *iface, IShellItemArray *items, LPWSTR *name);
static HRESULT STDMETHODCALLTYPE FT_GetIcon(IExplorerCommand *iface, IShellItemArray *items, LPWSTR *icon);
static HRESULT STDMETHODCALLTYPE FT_GetToolTip(IExplorerCommand *iface, IShellItemArray *items, LPWSTR *tip);
static HRESULT STDMETHODCALLTYPE FT_GetCanonicalName(IExplorerCommand *iface, GUID *guid);
static HRESULT STDMETHODCALLTYPE FT_GetState(IExplorerCommand *iface, IShellItemArray *items, BOOL slow, EXPCMDSTATE *state);
static HRESULT STDMETHODCALLTYPE FT_Invoke(IExplorerCommand *iface, IShellItemArray *items, IBindCtx *ctx);
static HRESULT STDMETHODCALLTYPE FT_GetFlags(IExplorerCommand *iface, EXPCMDFLAGS *flags);
static HRESULT STDMETHODCALLTYPE FT_EnumSubCommands(IExplorerCommand *iface, IEnumExplorerCommand **out);

static IExplorerCommandVtbl ft_vtbl = {
    FT_QueryInterface, FT_AddRef, FT_Release,
    FT_GetTitle, FT_GetIcon, FT_GetToolTip, FT_GetCanonicalName,
    FT_GetState, FT_Invoke, FT_GetFlags, FT_EnumSubCommands,
};

/* debug trace: writes one line per COM entry so a shell hang can be localised */
static void dbg(const char *m) {
    wchar_t path[MAX_PATH];
    DWORD n = GetEnvironmentVariableW(L"TEMP", path, MAX_PATH);
    if (n == 0 || n >= MAX_PATH - 20) return;
    wcscat(path, L"\\ftmenu.log");
    FILE *f = _wfopen(path, L"a");
    if (!f) return;
    fprintf(f, "%lu %s\n", GetTickCount(), m);
    fclose(f);
}

static BOOL icon_path(wchar_t *out, DWORD cap);

static HRESULT dup_wide(const wchar_t *s, LPWSTR *out) {
    size_t n = (wcslen(s) + 1) * sizeof(wchar_t);
    LPWSTR p = (LPWSTR)CoTaskMemAlloc(n);
    if (!p) return E_OUTOFMEMORY;
    memcpy(p, s, n);
    *out = p;
    return S_OK;
}

static HRESULT STDMETHODCALLTYPE FT_QueryInterface(IExplorerCommand *iface, REFIID riid, void **ppv) {
    dbg(__func__);
    if (!ppv) return E_POINTER;
    if (IsEqualIID(riid, &IID_IUnknown) || IsEqualIID(riid, &IID_IExplorerCommand)) {
        *ppv = iface;
        FT_AddRef(iface);
        return S_OK;
    }
    *ppv = NULL;
    return E_NOINTERFACE;
}

static ULONG STDMETHODCALLTYPE FT_AddRef(IExplorerCommand *iface) {
    dbg(__func__);
    FTMenu *self = (FTMenu *)iface;
    return (ULONG)InterlockedIncrement(&self->ref);
}

static ULONG STDMETHODCALLTYPE FT_Release(IExplorerCommand *iface) {
    dbg(__func__);
    FTMenu *self = (FTMenu *)iface;
    LONG r = InterlockedDecrement(&self->ref);
    if (r == 0) CoTaskMemFree(self);
    return (ULONG)r;
}

static HRESULT STDMETHODCALLTYPE FT_GetTitle(IExplorerCommand *iface, IShellItemArray *items, LPWSTR *name) {
    dbg(__func__);
    (void)iface; (void)items;
    if (!name) return E_POINTER;
    return dup_wide(MENU_TITLE, name);
}

static HRESULT STDMETHODCALLTYPE FT_GetIcon(IExplorerCommand *iface, IShellItemArray *items, LPWSTR *icon) {
    dbg(__func__);
    (void)iface; (void)items;
    if (!icon) return E_POINTER;
    wchar_t path[MAX_PATH];
    /* S_OK with *icon = NULL makes the shell wait for an image that never
       arrives (Explorer freezes); only answer S_OK with a real path. */
    if (!icon_path(path, MAX_PATH)) return E_NOTIMPL;
    return dup_wide(path, icon);
}

static HRESULT STDMETHODCALLTYPE FT_GetToolTip(IExplorerCommand *iface, IShellItemArray *items, LPWSTR *tip) {
    dbg(__func__);
    (void)iface; (void)items;
    if (!tip) return E_POINTER;
    return dup_wide(MENU_TIP, tip);
}

static HRESULT STDMETHODCALLTYPE FT_GetCanonicalName(IExplorerCommand *iface, GUID *guid) {
    dbg(__func__);
    (void)iface;
    if (!guid) return E_POINTER;
    *guid = CLSID_FTMenu;
    return S_OK;
}

static HRESULT STDMETHODCALLTYPE FT_GetState(IExplorerCommand *iface, IShellItemArray *items, BOOL slow, EXPCMDSTATE *state) {
    dbg(__func__);
    (void)iface; (void)slow;
    if (!state) return E_POINTER;
    *state = items ? ECS_ENABLED : ECS_HIDDEN;
    return S_OK;
}

static HRESULT STDMETHODCALLTYPE FT_GetFlags(IExplorerCommand *iface, EXPCMDFLAGS *flags) {
    dbg(__func__);
    (void)iface;
    if (!flags) return E_POINTER;
    *flags = ECF_DEFAULT;
    return S_OK;
}

static HRESULT STDMETHODCALLTYPE FT_EnumSubCommands(IExplorerCommand *iface, IEnumExplorerCommand **out) {
    dbg(__func__);
    (void)iface;
    if (out) *out = NULL;
    return E_NOTIMPL;
}

// --- launching -------------------------------------------------------------

static void append_quoted(char *buf, size_t cap, const wchar_t *path) {
    // Convert to UTF-8 and quote for the Windows command line.
    int need = WideCharToMultiByte(CP_UTF8, 0, path, -1, NULL, 0, NULL, NULL);
    if (need <= 0) return;
    char *u = (char *)malloc((size_t)need);
    if (!u) return;
    WideCharToMultiByte(CP_UTF8, 0, path, -1, u, need, NULL, NULL);

    size_t len = strlen(buf);
    if (len && len + 1 < cap) buf[len++] = ' ';
    if (len + 1 < cap) buf[len++] = '"';
    for (const char *p = u; *p && len + 2 < cap; p++) {
        if (*p == '"') { buf[len++] = '\\'; }
        buf[len++] = *p;
    }
    if (len + 1 < cap) buf[len++] = '"';
    buf[len] = 0;
    free(u);
}

static void desktop_exe(wchar_t *out, DWORD cap) {
    // Prefer the path the installer recorded, else the default install dir.
    HKEY k;
    if (RegOpenKeyExW(HKEY_CURRENT_USER, L"Software\\filetransfer", 0, KEY_READ, &k) == ERROR_SUCCESS) {
        DWORD size = cap * sizeof(wchar_t), type = 0;
        if (RegQueryValueExW(k, L"DesktopExe", NULL, &type, (LPBYTE)out, &size) == ERROR_SUCCESS && type == REG_SZ) {
            RegCloseKey(k);
            return;
        }
        RegCloseKey(k);
    }
    wchar_t *local = NULL;
    size_t n = 0;
    if (_wdupenv_s(&local, &n, L"LOCALAPPDATA") == 0 && local) {
        _snwprintf(out, cap, L"%s\\Ferry\\bin\\ferry-desktop.exe", local);
        free(local);
        return;
    }
    out[0] = 0;
}

static HRESULT STDMETHODCALLTYPE FT_Invoke(IExplorerCommand *iface, IShellItemArray *items, IBindCtx *ctx) {
    dbg(__func__);
    (void)iface; (void)ctx;
    if (!items) return E_INVALIDARG;

    DWORD count = 0;
    if (FAILED(items->lpVtbl->GetCount(items, &count)) || count == 0) return E_INVALIDARG;

    size_t cap = 64 * 1024;
    char *args = (char *)calloc(cap, 1);
    if (!args) return E_OUTOFMEMORY;

    for (DWORD i = 0; i < count; i++) {
        IShellItem *item = NULL;
        if (FAILED(items->lpVtbl->GetItemAt(items, i, &item)) || !item) continue;
        LPWSTR path = NULL;
        if (SUCCEEDED(item->lpVtbl->GetDisplayName(item, SIGDN_FILESYSPATH, &path)) && path) {
            append_quoted(args, cap, path);
            CoTaskMemFree(path);
        }
        item->lpVtbl->Release(item);
    }

    wchar_t exe[MAX_PATH * 2];
    desktop_exe(exe, MAX_PATH * 2);
    if (!exe[0]) {
        free(args);
        return E_FAIL;
    }

    wchar_t *wargs = NULL;
    int need = MultiByteToWideChar(CP_UTF8, 0, args, -1, NULL, 0);
    if (need > 0) {
        wargs = (wchar_t *)malloc((size_t)need * sizeof(wchar_t));
        if (wargs) MultiByteToWideChar(CP_UTF8, 0, args, -1, wargs, need);
    }
    free(args);

    HINSTANCE r = ShellExecuteW(NULL, L"open", exe, wargs, NULL, SW_SHOWNORMAL);
    free(wargs);
    return ((INT_PTR)r > 32) ? S_OK : E_FAIL;
}

// --- class factory ---------------------------------------------------------

typedef struct FTFactory FTFactory;
struct FTFactory { IClassFactoryVtbl *lpVtbl; LONG ref; };

static ULONG STDMETHODCALLTYPE F_AddRef(IClassFactory *iface);

static HRESULT STDMETHODCALLTYPE F_QueryInterface(IClassFactory *iface, REFIID riid, void **ppv) {
    dbg(__func__);
    if (!ppv) return E_POINTER;
    if (IsEqualIID(riid, &IID_IUnknown) || IsEqualIID(riid, &IID_IClassFactory)) {
        *ppv = iface;
        F_AddRef(iface);
        return S_OK;
    }
    *ppv = NULL;
    return E_NOINTERFACE;
}

static ULONG STDMETHODCALLTYPE F_AddRef(IClassFactory *iface) {
    dbg(__func__);
    FTFactory *self = (FTFactory *)iface;
    return (ULONG)InterlockedIncrement(&self->ref);
}

static ULONG STDMETHODCALLTYPE F_Release(IClassFactory *iface) {
    dbg(__func__);
    FTFactory *self = (FTFactory *)iface;
    LONG r = InterlockedDecrement(&self->ref);
    if (r == 0) CoTaskMemFree(self);
    return (ULONG)r;
}

static HRESULT STDMETHODCALLTYPE F_CreateInstance(IClassFactory *iface, IUnknown *outer, REFIID riid, void **ppv) {
    dbg(__func__);
    (void)iface;
    if (outer) return CLASS_E_NOAGGREGATION;
    if (!ppv) return E_POINTER;

    FTMenu *m = (FTMenu *)CoTaskMemAlloc(sizeof(FTMenu));
    if (!m) return E_OUTOFMEMORY;
    m->lpVtbl = &ft_vtbl;
    m->ref = 1;

    HRESULT hr = FT_QueryInterface((IExplorerCommand *)m, riid, ppv);
    FT_Release((IExplorerCommand *)m);
    return hr;
}

static HRESULT STDMETHODCALLTYPE F_LockServer(IClassFactory *iface, BOOL lock) {
    dbg(__func__);
    (void)iface; (void)lock;
    return S_OK;
}

static IClassFactoryVtbl factory_vtbl = {F_QueryInterface, F_AddRef, F_Release, F_CreateInstance, F_LockServer};

HRESULT __stdcall
DllGetClassObject(REFCLSID rclsid, REFIID riid, void **ppv) {
    if (!ppv) return E_POINTER;
    if (!IsEqualCLSID(rclsid, &CLSID_FTMenu)) return CLASS_E_CLASSNOTAVAILABLE;

    FTFactory *f = (FTFactory *)CoTaskMemAlloc(sizeof(FTFactory));
    if (!f) return E_OUTOFMEMORY;
    f->lpVtbl = &factory_vtbl;
    f->ref = 1;

    HRESULT hr = F_QueryInterface((IClassFactory *)f, riid, ppv);
    F_Release((IClassFactory *)f);
    return hr;
}

HRESULT __stdcall DllCanUnloadNow(void) { return S_FALSE; }

static HINSTANCE g_hinst;

BOOL WINAPI DllMain(HINSTANCE hinst, DWORD reason, LPVOID reserved) {
    (void)reason; (void)reserved;
    if (reason == DLL_PROCESS_ATTACH) g_hinst = hinst;
    return TRUE;
}

/* icon_path resolves ft.ico next to this DLL; returns FALSE when it is absent,
   in which case the shell should fall back to the verb's default icon. */
static BOOL icon_path(wchar_t *out, DWORD cap) {
    if (!g_hinst || !GetModuleFileNameW(g_hinst, out, cap)) return FALSE;
    wchar_t *slash = wcsrchr(out, L'\\');
    if (!slash) return FALSE;
    wcscpy(slash + 1, L"ft.ico");
    DWORD attr = GetFileAttributesW(out);
    return attr != INVALID_FILE_ATTRIBUTES && !(attr & FILE_ATTRIBUTE_DIRECTORY);
}
