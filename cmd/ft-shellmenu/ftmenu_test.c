// ftmenu_test.exe — loads ftmenu.dll and exercises IExplorerCommand with a real
// IShellItemArray, outside of Explorer, so a crash does not take the shell down.
#define WIN32_LEAN_AND_MEAN
#include <windows.h>
#include <shobjidl.h>
#include <shlobj.h>
#include <stdio.h>

typedef HRESULT (__stdcall *PFN_GetClassObject)(REFCLSID, REFIID, void **);

static const GUID CLSID_FTMenu = {
    0xB7F1D2C4, 0x8A31, 0x4C77, {0x9D, 0x2E, 0x3A, 0x5B, 0x7C, 0x1E, 0x9F, 0x10}};

static void step(const char *name, HRESULT hr) {
    printf("%-18s hr=0x%08lx\n", name, (unsigned long)hr);
    fflush(stdout);
}

int wmain(int argc, wchar_t **argv) {
    const wchar_t *dll = argc > 1 ? argv[1] : L"ftmenu.dll";
    const wchar_t *file = argc > 2 ? argv[2] : L"hello.txt";

    CoInitializeEx(NULL, COINIT_APARTMENTTHREADED);

    HMODULE h = LoadLibraryW(dll);
    if (!h) { printf("LoadLibrary failed %lu\n", GetLastError()); return 2; }
    PFN_GetClassObject get = (PFN_GetClassObject)GetProcAddress(h, "DllGetClassObject");
    if (!get) { printf("no DllGetClassObject\n"); return 2; }
    printf("dll loaded\n"); fflush(stdout);

    IClassFactory *fac = NULL;
    step("DllGetClassObject", get(&CLSID_FTMenu, &IID_IClassFactory, (void **)&fac));
    if (!fac) { printf("no factory\n"); return 2; }

    IExplorerCommand *cmd = NULL;
    step("CreateInstance", fac->lpVtbl->CreateInstance(fac, NULL, &IID_IExplorerCommand, (void **)&cmd));
    if (!cmd) { printf("no command\n"); return 2; }
    printf("command created\n"); fflush(stdout);

    IShellItem *item = NULL;
    step("SHCreateItemFromParsingName", SHCreateItemFromParsingName(file, NULL, &IID_IShellItem, (void **)&item));
    IShellItemArray *arr = NULL;
    if (item) step("SHCreateShellItemArrayFromShellItem", SHCreateShellItemArrayFromShellItem(item, &IID_IShellItemArray, (void **)&arr));
    printf("item=%p arr=%p\n", (void *)item, (void *)arr); fflush(stdout);

    LPWSTR s = NULL;
    step("GetTitle", cmd->lpVtbl->GetTitle(cmd, arr, &s));
    if (s) { printf("  title=%ls\n", s); CoTaskMemFree(s); s = NULL; }
    step("GetToolTip", cmd->lpVtbl->GetToolTip(cmd, arr, &s));
    if (s) { printf("  tip=%ls\n", s); CoTaskMemFree(s); s = NULL; }
    step("GetIcon", cmd->lpVtbl->GetIcon(cmd, arr, &s));
    if (s) CoTaskMemFree(s);
    GUID g = {0};
    step("GetCanonicalName", cmd->lpVtbl->GetCanonicalName(cmd, &g));
    EXPCMDSTATE st = 0;
    step("GetState", cmd->lpVtbl->GetState(cmd, arr, FALSE, &st));
    printf("  state=0x%lx\n", (unsigned long)st);
    EXPCMDFLAGS fl = 0;
    step("GetFlags", cmd->lpVtbl->GetFlags(cmd, &fl));
    printf("  flags=0x%lx\n", (unsigned long)fl);
    IEnumExplorerCommand *en = NULL;
    step("EnumSubCommands", cmd->lpVtbl->EnumSubCommands(cmd, &en));

    printf("all calls returned\n");

    if (argc > 3 && !wcscmp(argv[3], L"--invoke")) {
        step("Invoke", cmd->lpVtbl->Invoke(cmd, arr, NULL));
        Sleep(1500);
    }

    if (en) en->lpVtbl->Release(en);
    if (arr) arr->lpVtbl->Release(arr);
    if (item) item->lpVtbl->Release(item);
    cmd->lpVtbl->Release(cmd);
    fac->lpVtbl->Release(fac);
    CoUninitialize();
    printf("done\n");
    return 0;
}
