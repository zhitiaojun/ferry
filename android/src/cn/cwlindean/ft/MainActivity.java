package cn.cwlindean.ft;

import android.animation.ValueAnimator;
import android.app.Activity;
import android.app.AlertDialog;
import android.content.ClipData;
import android.content.ClipboardManager;
import android.content.ContentValues;
import android.content.Intent;
import android.content.SharedPreferences;
import android.content.res.ColorStateList;
import android.content.res.Configuration;
import android.database.Cursor;
import android.graphics.Canvas;
import android.graphics.Color;
import android.graphics.ColorFilter;
import android.graphics.Paint;
import android.graphics.Path;
import android.graphics.PixelFormat;
import android.graphics.Typeface;
import android.graphics.drawable.Drawable;
import android.graphics.drawable.GradientDrawable;
import android.graphics.drawable.RippleDrawable;
import android.net.Uri;
import android.net.wifi.WifiManager;
import android.os.Build;
import android.os.Bundle;
import android.os.Environment;
import android.os.Handler;
import android.os.Looper;
import android.provider.MediaStore;
import android.provider.OpenableColumns;
import android.provider.Settings;
import android.text.Editable;
import android.text.InputType;
import android.text.TextUtils;
import android.text.TextWatcher;
import android.util.Log;
import android.view.Gravity;
import android.view.View;
import android.view.ViewGroup;
import android.view.animation.AccelerateDecelerateInterpolator;
import android.webkit.MimeTypeMap;
import android.widget.Button;
import android.widget.EditText;
import android.widget.ImageView;
import android.widget.LinearLayout;
import android.widget.ScrollView;
import android.widget.TextView;
import android.widget.Toast;

import org.json.JSONArray;
import org.json.JSONObject;

import java.io.ByteArrayOutputStream;
import java.io.File;
import java.io.FileInputStream;
import java.io.FileOutputStream;
import java.io.InputStream;
import java.io.OutputStream;
import java.net.Inet4Address;
import java.net.InetAddress;
import java.net.InterfaceAddress;
import java.net.NetworkInterface;
import java.text.SimpleDateFormat;
import java.util.ArrayList;
import java.util.Collections;
import java.util.Date;
import java.util.List;
import java.util.Locale;
import java.util.concurrent.ExecutorService;
import java.util.concurrent.Executors;
import java.util.regex.Pattern;

import mobile.Mobile;

/**
 * Material Design 3 UI shell for ft. Transport, discovery, relay signaling and
 * crypto identity live in the Go AAR (core/mobile).
 */
public class MainActivity extends Activity {
    private static final String TAG = "ft";
    private static final String SIGNAL_URL = "wss://ft.cwlindean.cn/signal";
    private static final int PICK_FILES = 42;
    private static final int PICK_PAIR_FILE = 43;
    private static final int MATCH = ViewGroup.LayoutParams.MATCH_PARENT;
    private static final int WRAP = ViewGroup.LayoutParams.WRAP_CONTENT;
    private static final Pattern HEX64 = Pattern.compile("^[0-9a-fA-F]{64}$");

    // MD3 tonal roles (Dynamic on Android 12+, refined sage palette fallback).
    private int cPrimary, cOnPrimary, cPrimaryContainer, cOnPrimaryContainer;
    private int cSecondaryContainer, cOnSecondaryContainer;
    private int cTertiaryContainer, cOnTertiaryContainer;
    private int cSurface, cSurfaceContainerLow, cSurfaceContainer, cSurfaceContainerHigh;
    private int cOnSurface, cOnSurfaceVariant, cOutline, cOutlineVariant;
    private int cError, cOnError, cErrorContainer, cOnErrorContainer;
    private int cWarn, cWarnContainer, cRipple;

    // App state
    private final Handler ui = new Handler(Looper.getMainLooper());
    private final ExecutorService ioPool = Executors.newSingleThreadExecutor();
    private WifiManager.MulticastLock multicastLock;
    private String deviceName;
    private String fullDeviceId = "";
    private String webShareUrl = "";
    private boolean recvRunning;
    private boolean webRunning;
    private boolean recvEverStarted;
    private int activeTab;
    private String lanPort = "";
    private List<Uri> sharedUris;
    private final List<PeerEntry> pairedEntries = new ArrayList<>();
    private final List<PeerEntry> lanEntries = new ArrayList<>();
    private final List<ReceivedEntry> recentEntries = new ArrayList<>();
    private String lanNote = "";

    // Top bar & bottom nav
    private LinearLayout topChip;
    private View topDot;
    private TextView topStateText;
    private TextView topIdChip;
    private final LinearLayout[] navItems = new LinearLayout[3];
    private final View[] navPills = new View[3];
    private final ImageView[] navIcons = new ImageView[3];
    private final TextView[] navLabels = new TextView[3];
    private View panelSend, panelRecv, panelPair;
    private ValueAnimator dotAnimator;

    // Send tab views
    private TextView targetTitleView;
    private TextView targetSubView;
    private TextView shareBannerView;
    private Button sendPickBtn;
    private Button sendClipBtn;
    private LinearLayout sendResultCard;
    private TextView sendResultBadge;
    private TextView sendResultText;
    private Button discoverBtn;
    private DeviceList deviceList;
    private EditText peerField;

    // Receive tab views
    private LinearLayout recvHeroCard;
    private TextView recvHeroTitle;
    private TextView recvHeroSub;
    private Button recvToggleBtn;
    private Button webToggleBtn;
    private TextView webUrlView;
    private LinearLayout recentListView;

    // Pair tab views
    private EditText nameField;
    private TextView pairFullIdView;
    private EditText pakeCodeField;
    private EditText pakePeerField;
    private TextView pakeInfoView;
    private EditText pairUriField;

    private final Runnable pollTask = new Runnable() {
        @Override
        public void run() {
            pollReceivedOnce();
            if (recvRunning) {
                ui.postDelayed(this, 1500);
            }
        }
    };

    @Override
    protected void onCreate(Bundle savedInstanceState) {
        super.onCreate(savedInstanceState);
        loadMd3Tokens();
        applyWindowChrome();
        migrateLegacyState();
        deviceName = loadSavedName();
        setContentView(buildUi());

        if (Build.VERSION.SDK_INT >= 33) {
            requestPermissions(new String[]{"android.permission.POST_NOTIFICATIONS"}, 7);
        }
        importBootstrapPairs();
        applyDebugPeerId();
        prefillLastPeer();
        loadDeviceId();
        loadPairedPeers();
        loadRecentState();
        sweepLeftoverInbox();
        handleShareIntent(getIntent());
    }

    @Override
    protected void onResume() {
        super.onResume();
        pollReceivedOnce();
    }

    @Override
    protected void onNewIntent(Intent intent) {
        super.onNewIntent(intent);
        setIntent(intent);
        handleShareIntent(intent);
    }

    @Override
    protected void onDestroy() {
        ui.removeCallbacks(pollTask);
        stopDotPulse();
        releaseMulticastLock();
        ioPool.shutdownNow();
        super.onDestroy();
    }

    // ================= App Directories (Key Isolation) =================

    /** Internal private dir holding device.key, paired.json, logs, and recent index. */
    private File stateDir() {
        File d = new File(getFilesDir(), "state");
        if (!d.exists()) {
            d.mkdirs();
        }
        return d;
    }

    /** Internal staging dir for incoming Go transfers before moving to Download/ft. */
    private File inboxDir() {
        File d = new File(getFilesDir(), "inbox");
        if (!d.exists()) {
            d.mkdirs();
        }
        return d;
    }

    /** Separate directory for LAN Web Share so private keys are never exposed. */
    private File webShareDir() {
        File d = new File(getFilesDir(), "webshare");
        if (!d.exists()) {
            d.mkdirs();
        }
        return d;
    }

    private File keyFile() {
        return new File(stateDir(), "device.key");
    }

    private File pairedFile() {
        return new File(stateDir(), "paired.json");
    }

    private File recentFile() {
        return new File(stateDir(), "recent.json");
    }

    /** Preserves existing identity/pairings from older v0.8 external received/ layout. */
    private void migrateLegacyState() {
        File ext = getExternalFilesDir(null);
        if (ext == null) {
            return;
        }
        File legacyDir = new File(ext, "received");
        copyIfMissing(new File(legacyDir, "device.key"), keyFile());
        copyIfMissing(new File(legacyDir, "paired.json"), pairedFile());
    }

    private static void copyIfMissing(File src, File dst) {
        if (!src.isFile() || dst.isFile()) {
            return;
        }
        try (InputStream in = new FileInputStream(src);
             OutputStream out = new FileOutputStream(dst)) {
            byte[] buf = new byte[8192];
            int n;
            while ((n = in.read(buf)) > 0) {
                out.write(buf, 0, n);
            }
        } catch (Exception e) {
            Log.w(TAG, "migrate " + src.getName(), e);
        }
    }

    // ================= MD3 Tokens & Chrome =================

    private boolean isNight() {
        return (getResources().getConfiguration().uiMode & Configuration.UI_MODE_NIGHT_MASK)
                == Configuration.UI_MODE_NIGHT_YES;
    }

    private int sysColor(int resId, int fallback) {
        if (Build.VERSION.SDK_INT >= 31) {
            try {
                return getColor(resId);
            } catch (Exception ignored) {
            }
        }
        return fallback;
    }

    private void loadMd3Tokens() {
        boolean night = isNight();
        if (Build.VERSION.SDK_INT >= 31) {
            if (night) {
                cPrimary = sysColor(android.R.color.system_accent1_200, 0xFF8CD6B1);
                cOnPrimary = sysColor(android.R.color.system_accent1_800, 0xFF003824);
                cPrimaryContainer = sysColor(android.R.color.system_accent1_700, 0xFF0A5138);
                cOnPrimaryContainer = sysColor(android.R.color.system_accent1_100, 0xFFA8F2CC);
                cSecondaryContainer = sysColor(android.R.color.system_accent2_700, 0xFF354B40);
                cOnSecondaryContainer = sysColor(android.R.color.system_accent2_100, 0xFFCFE9DA);
                cTertiaryContainer = sysColor(android.R.color.system_accent3_700, 0xFF2B4A58);
                cOnTertiaryContainer = sysColor(android.R.color.system_accent3_100, 0xFFC4E7F8);
                cSurface = sysColor(android.R.color.system_neutral1_900, 0xFF101512);
                cSurfaceContainerLow = Color.parseColor("#181D1A");
                cSurfaceContainer = Color.parseColor("#1C221E");
                cSurfaceContainerHigh = Color.parseColor("#262D28");
                cOnSurface = sysColor(android.R.color.system_neutral1_100, 0xFFE0E4E0);
                cOnSurfaceVariant = sysColor(android.R.color.system_neutral2_200, 0xFFBFC9C2);
                cOutline = sysColor(android.R.color.system_neutral2_400, 0xFF89938C);
                cOutlineVariant = sysColor(android.R.color.system_neutral2_700, 0xFF404943);
            } else {
                cPrimary = sysColor(android.R.color.system_accent1_600, 0xFF1D6B4E);
                cOnPrimary = Color.WHITE;
                cPrimaryContainer = sysColor(android.R.color.system_accent1_100, 0xFFA8F2CC);
                cOnPrimaryContainer = sysColor(android.R.color.system_accent1_900, 0xFF002114);
                cSecondaryContainer = sysColor(android.R.color.system_accent2_100, 0xFFCFE9DA);
                cOnSecondaryContainer = sysColor(android.R.color.system_accent2_900, 0xFF0B1F16);
                cTertiaryContainer = sysColor(android.R.color.system_accent3_100, 0xFFC4E7F8);
                cOnTertiaryContainer = sysColor(android.R.color.system_accent3_900, 0xFF001F29);
                cSurface = sysColor(android.R.color.system_neutral1_10, 0xFFF7FAF7);
                cSurfaceContainerLow = sysColor(android.R.color.system_neutral1_50, 0xFFF0F5F0);
                cSurfaceContainer = Color.parseColor("#E9EFEA");
                cSurfaceContainerHigh = sysColor(android.R.color.system_neutral1_100, 0xFFE2E9E3);
                cOnSurface = sysColor(android.R.color.system_neutral1_900, 0xFF181D1A);
                cOnSurfaceVariant = sysColor(android.R.color.system_neutral2_700, 0xFF404943);
                cOutline = sysColor(android.R.color.system_neutral2_500, 0xFF707973);
                cOutlineVariant = sysColor(android.R.color.system_neutral2_200, 0xFFBFC9C2);
            }
        } else if (night) {
            cPrimary = Color.parseColor("#8CD6B1");
            cOnPrimary = Color.parseColor("#003824");
            cPrimaryContainer = Color.parseColor("#0A5138");
            cOnPrimaryContainer = Color.parseColor("#A8F2CC");
            cSecondaryContainer = Color.parseColor("#354B40");
            cOnSecondaryContainer = Color.parseColor("#CFE9DA");
            cTertiaryContainer = Color.parseColor("#2B4A58");
            cOnTertiaryContainer = Color.parseColor("#C4E7F8");
            cSurface = Color.parseColor("#101512");
            cSurfaceContainerLow = Color.parseColor("#181D1A");
            cSurfaceContainer = Color.parseColor("#1C221E");
            cSurfaceContainerHigh = Color.parseColor("#262D28");
            cOnSurface = Color.parseColor("#E0E4E0");
            cOnSurfaceVariant = Color.parseColor("#BFC9C2");
            cOutline = Color.parseColor("#89938C");
            cOutlineVariant = Color.parseColor("#404943");
        } else {
            cPrimary = Color.parseColor("#1D6B4E");
            cOnPrimary = Color.WHITE;
            cPrimaryContainer = Color.parseColor("#A8F2CC");
            cOnPrimaryContainer = Color.parseColor("#002114");
            cSecondaryContainer = Color.parseColor("#CFE9DA");
            cOnSecondaryContainer = Color.parseColor("#0B1F16");
            cTertiaryContainer = Color.parseColor("#C4E7F8");
            cOnTertiaryContainer = Color.parseColor("#001F29");
            cSurface = Color.parseColor("#F7FAF7");
            cSurfaceContainerLow = Color.parseColor("#EFF5F0");
            cSurfaceContainer = Color.parseColor("#E8EFEA");
            cSurfaceContainerHigh = Color.parseColor("#DFE8E2");
            cOnSurface = Color.parseColor("#181D1A");
            cOnSurfaceVariant = Color.parseColor("#404943");
            cOutline = Color.parseColor("#707973");
            cOutlineVariant = Color.parseColor("#BFC9C2");
        }

        if (night) {
            cError = Color.parseColor("#FFB4AB");
            cOnError = Color.parseColor("#690005");
            cErrorContainer = Color.parseColor("#93000A");
            cOnErrorContainer = Color.parseColor("#FFDAD6");
            cWarn = Color.parseColor("#F4BF6A");
            cWarnContainer = Color.parseColor("#4C3304");
            cRipple = Color.parseColor("#28FFFFFF");
        } else {
            cError = Color.parseColor("#BA1A1A");
            cOnError = Color.WHITE;
            cErrorContainer = Color.parseColor("#FFDAD6");
            cOnErrorContainer = Color.parseColor("#410002");
            cWarn = Color.parseColor("#8A5600");
            cWarnContainer = Color.parseColor("#FFE2B7");
            cRipple = Color.parseColor("#1A000000");
        }
    }

    private void applyWindowChrome() {
        try {
            getWindow().setStatusBarColor(cSurface);
            getWindow().setNavigationBarColor(cSurfaceContainer);
            View dv = getWindow().getDecorView();
            int flags = dv.getSystemUiVisibility();
            if (isNight()) {
                flags &= ~View.SYSTEM_UI_FLAG_LIGHT_STATUS_BAR;
            } else {
                flags |= View.SYSTEM_UI_FLAG_LIGHT_STATUS_BAR;
            }
            if (Build.VERSION.SDK_INT >= 26) {
                if (isNight()) {
                    flags &= ~View.SYSTEM_UI_FLAG_LIGHT_NAVIGATION_BAR;
                } else {
                    flags |= View.SYSTEM_UI_FLAG_LIGHT_NAVIGATION_BAR;
                }
            }
            dv.setSystemUiVisibility(flags);
        } catch (Exception e) {
            Log.w(TAG, "chrome", e);
        }
    }

    // ================= Build MD3 Scaffold =================

    private View buildUi() {
        LinearLayout scaffold = new LinearLayout(this);
        scaffold.setOrientation(LinearLayout.VERTICAL);
        scaffold.setBackgroundColor(cSurface);

        scaffold.addView(buildTopBar(), new LinearLayout.LayoutParams(MATCH, WRAP));

        ScrollView scroller = new ScrollView(this);
        scroller.setFillViewport(true);
        scroller.setVerticalScrollBarEnabled(false);

        LinearLayout content = new LinearLayout(this);
        content.setOrientation(LinearLayout.VERTICAL);
        content.setPadding(dp(16), dp(4), dp(16), dp(20));

        LinearLayout send = new LinearLayout(this);
        send.setOrientation(LinearLayout.VERTICAL);
        buildSendPanel(send);
        panelSend = send;
        content.addView(send, new LinearLayout.LayoutParams(MATCH, WRAP));

        LinearLayout recv = new LinearLayout(this);
        recv.setOrientation(LinearLayout.VERTICAL);
        buildReceivePanel(recv);
        panelRecv = recv;
        content.addView(recv, new LinearLayout.LayoutParams(MATCH, WRAP));

        LinearLayout pair = new LinearLayout(this);
        pair.setOrientation(LinearLayout.VERTICAL);
        buildPairPanel(pair);
        panelPair = pair;
        content.addView(pair, new LinearLayout.LayoutParams(MATCH, WRAP));

        scroller.addView(content, new ScrollView.LayoutParams(MATCH, WRAP));
        scaffold.addView(scroller, new LinearLayout.LayoutParams(MATCH, 0, 1f));

        View divider = new View(this);
        divider.setBackgroundColor(cOutlineVariant);
        scaffold.addView(divider, new LinearLayout.LayoutParams(MATCH, Math.max(1, dp(0.5f))));

        scaffold.addView(buildBottomNav(), new LinearLayout.LayoutParams(MATCH, dp(72)));

        selectTab(0);
        return scaffold;
    }

    private LinearLayout buildTopBar() {
        LinearLayout bar = new LinearLayout(this);
        bar.setOrientation(LinearLayout.HORIZONTAL);
        bar.setGravity(Gravity.CENTER_VERTICAL);
        bar.setPadding(dp(20), dp(14), dp(16), dp(12));

        TextView title = label("轻渡", 22, cOnSurface, true);
        title.setTypeface(Typeface.create("sans-serif-medium", Typeface.BOLD));
        bar.addView(title, new LinearLayout.LayoutParams(WRAP, WRAP));

        topIdChip = label("ID —", 11, cOnSurfaceVariant, false);
        topIdChip.setTypeface(Typeface.MONOSPACE);
        topIdChip.setPadding(dp(10), dp(5), dp(10), dp(5));
        topIdChip.setBackground(ripple(roundBox(cSurfaceContainerHigh, 0, 99), 99));
        topIdChip.setOnClickListener(v -> copyText("设备 ID", fullDeviceId, "已复制完整设备 ID"));
        LinearLayout.LayoutParams idLp = new LinearLayout.LayoutParams(WRAP, WRAP);
        idLp.leftMargin = dp(10);
        bar.addView(topIdChip, idLp);

        bar.addView(new View(this), new LinearLayout.LayoutParams(0, dp(1), 1f));

        topChip = new LinearLayout(this);
        topChip.setOrientation(LinearLayout.HORIZONTAL);
        topChip.setGravity(Gravity.CENTER_VERTICAL);
        topChip.setPadding(dp(10), dp(6), dp(12), dp(6));
        topChip.setBackground(ripple(roundBox(cSurfaceContainerHigh, 0, 99), 99));
        topChip.setOnClickListener(v -> selectTab(1));

        topDot = new View(this);
        topDot.setBackground(oval(cOnSurfaceVariant));
        topChip.addView(topDot, new LinearLayout.LayoutParams(dp(8), dp(8)));

        topStateText = label("未开启", 12, cOnSurfaceVariant, true);
        LinearLayout.LayoutParams stLp = new LinearLayout.LayoutParams(WRAP, WRAP);
        stLp.leftMargin = dp(6);
        topChip.addView(topStateText, stLp);

        bar.addView(topChip, new LinearLayout.LayoutParams(WRAP, WRAP));
        return bar;
    }

    private LinearLayout buildBottomNav() {
        LinearLayout nav = new LinearLayout(this);
        nav.setOrientation(LinearLayout.HORIZONTAL);
        nav.setBackgroundColor(cSurfaceContainer);
        nav.setGravity(Gravity.CENTER_VERTICAL);

        final String[] labels = {"发送", "接收", "设备"};
        for (int i = 0; i < 3; i++) {
            final int idx = i;
            LinearLayout item = new LinearLayout(this);
            item.setOrientation(LinearLayout.VERTICAL);
            item.setGravity(Gravity.CENTER);
            item.setClickable(true);
            item.setFocusable(true);
            item.setOnClickListener(v -> selectTab(idx));

            LinearLayout pill = new LinearLayout(this);
            pill.setGravity(Gravity.CENTER);
            ImageView icon = new ImageView(this);
            pill.addView(icon, new LinearLayout.LayoutParams(dp(20), dp(20)));
            item.addView(pill, new LinearLayout.LayoutParams(dp(64), dp(32)));

            TextView lbl = label(labels[i], 12, cOnSurfaceVariant, false);
            LinearLayout.LayoutParams lLp = new LinearLayout.LayoutParams(WRAP, WRAP);
            lLp.topMargin = dp(4);
            item.addView(lbl, lLp);

            navItems[i] = item;
            navPills[i] = pill;
            navIcons[i] = icon;
            navLabels[i] = lbl;
            nav.addView(item, new LinearLayout.LayoutParams(0, MATCH, 1f));
        }
        return nav;
    }

    private void selectTab(int index) {
        activeTab = index;
        View[] panels = {panelSend, panelRecv, panelPair};
        for (int i = 0; i < 3; i++) {
            boolean on = i == index;
            if (panels[i] != null) {
                panels[i].setVisibility(on ? View.VISIBLE : View.GONE);
            }
            if (navPills[i] != null) {
                navPills[i].setBackground(roundBox(on ? cSecondaryContainer : Color.TRANSPARENT, 0, 16));
            }
            if (navIcons[i] != null) {
                navIcons[i].setImageDrawable(new NavIcon(i, on ? cOnSecondaryContainer : cOnSurfaceVariant));
            }
            if (navLabels[i] != null) {
                navLabels[i].setTextColor(on ? cOnSurface : cOnSurfaceVariant);
                navLabels[i].setTypeface(on
                        ? Typeface.create("sans-serif-medium", Typeface.BOLD)
                        : Typeface.DEFAULT);
            }
        }
    }

    // ================= Tab 0: Send =================

    private void buildSendPanel(LinearLayout root) {
        LinearLayout hero = md3Card(cSurfaceContainerLow, 24, 20, 20, 20, 20);
        root.addView(hero, new LinearLayout.LayoutParams(MATCH, WRAP));

        hero.addView(label("发往目标", 12, cOnSurfaceVariant, true),
                new LinearLayout.LayoutParams(WRAP, WRAP));

        targetTitleView = label("尚未选择接收设备", 18, cOnSurface, true);
        LinearLayout.LayoutParams ttLp = new LinearLayout.LayoutParams(MATCH, WRAP);
        ttLp.topMargin = dp(4);
        hero.addView(targetTitleView, ttLp);

        targetSubView = label("可在下方列表点击已配对或局域网设备", 12, cOnSurfaceVariant, false);
        LinearLayout.LayoutParams tsLp = new LinearLayout.LayoutParams(MATCH, WRAP);
        tsLp.topMargin = dp(3);
        hero.addView(targetSubView, tsLp);

        shareBannerView = label("", 12, cOnSecondaryContainer, true);
        shareBannerView.setBackground(roundBox(cSecondaryContainer, 0, 12));
        shareBannerView.setPadding(dp(12), dp(10), dp(12), dp(10));
        shareBannerView.setVisibility(View.GONE);
        LinearLayout.LayoutParams sbLp = new LinearLayout.LayoutParams(MATCH, WRAP);
        sbLp.topMargin = dp(12);
        hero.addView(shareBannerView, sbLp);

        sendPickBtn = pillFilledBtn("选择文件发送", cPrimary, cOnPrimary);
        sendPickBtn.setOnClickListener(v -> pickFiles());
        LinearLayout.LayoutParams spLp = new LinearLayout.LayoutParams(MATCH, dp(52));
        spLp.topMargin = dp(16);
        hero.addView(sendPickBtn, spLp);

        sendClipBtn = pillFilledBtn("发送剪贴板文本", cSecondaryContainer, cOnSecondaryContainer);
        sendClipBtn.setOnClickListener(v -> sendClipboard());
        LinearLayout.LayoutParams scLp = new LinearLayout.LayoutParams(MATCH, dp(44));
        scLp.topMargin = dp(10);
        hero.addView(sendClipBtn, scLp);

        sendResultCard = new LinearLayout(this);
        sendResultCard.setOrientation(LinearLayout.HORIZONTAL);
        sendResultCard.setGravity(Gravity.CENTER_VERTICAL);
        sendResultCard.setPadding(dp(12), dp(10), dp(12), dp(10));
        sendResultCard.setBackground(roundBox(cSurfaceContainerHigh, 0, 14));
        sendResultCard.setVisibility(View.GONE);

        sendResultBadge = label("", 11, cOnPrimaryContainer, true);
        sendResultBadge.setPadding(dp(8), dp(3), dp(8), dp(3));
        sendResultBadge.setBackground(roundBox(cPrimaryContainer, 0, 99));
        sendResultBadge.setVisibility(View.GONE);
        sendResultCard.addView(sendResultBadge, new LinearLayout.LayoutParams(WRAP, WRAP));

        sendResultText = label("", 12, cOnSurface, false);
        LinearLayout.LayoutParams rtLp = new LinearLayout.LayoutParams(0, WRAP, 1f);
        rtLp.leftMargin = dp(8);
        sendResultCard.addView(sendResultText, rtLp);

        LinearLayout.LayoutParams srLp = new LinearLayout.LayoutParams(MATCH, WRAP);
        srLp.topMargin = dp(12);
        hero.addView(sendResultCard, srLp);

        // Available devices card
        LinearLayout devCard = md3Card(cSurfaceContainerLow, 20, 16, 16, 16, 16);
        LinearLayout.LayoutParams dcLp = new LinearLayout.LayoutParams(MATCH, WRAP);
        dcLp.topMargin = dp(14);
        root.addView(devCard, dcLp);

        LinearLayout devHead = new LinearLayout(this);
        devHead.setOrientation(LinearLayout.HORIZONTAL);
        devHead.setGravity(Gravity.CENTER_VERTICAL);
        devHead.addView(label("可用设备", 15, cOnSurface, true),
                new LinearLayout.LayoutParams(0, WRAP, 1f));

        discoverBtn = pillSmallBtn("搜索局域网", cSecondaryContainer, cOnSecondaryContainer);
        discoverBtn.setOnClickListener(v -> discover());
        devHead.addView(discoverBtn, new LinearLayout.LayoutParams(WRAP, dp(34)));
        devCard.addView(devHead, new LinearLayout.LayoutParams(MATCH, WRAP));

        deviceList = new DeviceList();
        LinearLayout.LayoutParams dlLp = new LinearLayout.LayoutParams(MATCH, WRAP);
        dlLp.topMargin = dp(10);
        devCard.addView(deviceList, dlLp);

        // Manual input card
        LinearLayout manCard = md3Card(cSurfaceContainerLow, 20, 16, 14, 16, 16);
        LinearLayout.LayoutParams mcLp = new LinearLayout.LayoutParams(MATCH, WRAP);
        mcLp.topMargin = dp(14);
        root.addView(manCard, mcLp);

        manCard.addView(label("手动填写目标（设备 ID 或 IP:端口）", 12, cOnSurfaceVariant, true),
                new LinearLayout.LayoutParams(WRAP, WRAP));

        peerField = md3Input("粘贴 64 位设备 ID，或输入 IP:端口");
        peerField.addTextChangedListener(new TextWatcher() {
            @Override public void beforeTextChanged(CharSequence s, int st, int c, int a) {}
            @Override public void onTextChanged(CharSequence s, int st, int b, int c) {}
            @Override public void afterTextChanged(Editable s) {
                updateTargetSummary();
                if (deviceList != null) {
                    deviceList.syncSelection();
                }
            }
        });
        LinearLayout.LayoutParams pfLp = new LinearLayout.LayoutParams(MATCH, dp(48));
        pfLp.topMargin = dp(8);
        manCard.addView(peerField, pfLp);
        deviceList.render(pairedEntries, lanEntries, lanNote);
    }

    // ================= Tab 1: Receive & Files =================

    private void buildReceivePanel(LinearLayout root) {
        recvHeroCard = md3Card(cSurfaceContainerLow, 24, 20, 20, 20, 20);
        root.addView(recvHeroCard, new LinearLayout.LayoutParams(MATCH, WRAP));

        recvHeroTitle = label("准备接收文件", 20, cOnSurface, true);
        recvHeroCard.addView(recvHeroTitle, new LinearLayout.LayoutParams(MATCH, WRAP));

        recvHeroSub = label("点击下方按钮后同时开启局域网与远程自动选路，收到文件自动保存到系统「下载 / Ferry」",
                13, cOnSurfaceVariant, false);
        recvHeroSub.setLineSpacing(dp(4), 1f);
        LinearLayout.LayoutParams hsLp = new LinearLayout.LayoutParams(MATCH, WRAP);
        hsLp.topMargin = dp(6);
        recvHeroCard.addView(recvHeroSub, hsLp);

        recvToggleBtn = pillFilledBtn("开启接收", cPrimary, cOnPrimary);
        recvToggleBtn.setOnClickListener(v -> toggleReceive());
        LinearLayout.LayoutParams rtLp = new LinearLayout.LayoutParams(MATCH, dp(52));
        rtLp.topMargin = dp(18);
        recvHeroCard.addView(recvToggleBtn, rtLp);

        // Tools: Web share & background optimization
        LinearLayout toolsCard = md3Card(cSurfaceContainerLow, 20, 16, 16, 16, 16);
        LinearLayout.LayoutParams tcLp = new LinearLayout.LayoutParams(MATCH, WRAP);
        tcLp.topMargin = dp(14);
        root.addView(toolsCard, tcLp);

        toolsCard.addView(label("网页快传与保活", 15, cOnSurface, true),
                new LinearLayout.LayoutParams(WRAP, WRAP));
        LinearLayout.LayoutParams wSubLp = new LinearLayout.LayoutParams(WRAP, WRAP);
        wSubLp.topMargin = dp(3);
        toolsCard.addView(label("网页分享仅共享独立临时目录，不包含设备密钥与配对表", 12, cOnSurfaceVariant, false), wSubLp);

        LinearLayout btnRow = new LinearLayout(this);
        btnRow.setOrientation(LinearLayout.HORIZONTAL);
        webToggleBtn = pillFilledBtn("开启网页分享", cSecondaryContainer, cOnSecondaryContainer);
        webToggleBtn.setOnClickListener(v -> toggleWebShare());
        Button battBtn = pillOutlineBtn("允许后台保活");
        battBtn.setOnClickListener(v -> requestBatteryExemption());
        btnRow.addView(webToggleBtn, new LinearLayout.LayoutParams(0, dp(44), 1f));
        LinearLayout.LayoutParams b2Lp = new LinearLayout.LayoutParams(0, dp(44), 1f);
        b2Lp.leftMargin = dp(10);
        btnRow.addView(battBtn, b2Lp);
        LinearLayout.LayoutParams rowLp = new LinearLayout.LayoutParams(MATCH, WRAP);
        rowLp.topMargin = dp(12);
        toolsCard.addView(btnRow, rowLp);

        webUrlView = label("", 12, cOnPrimaryContainer, true);
        webUrlView.setTypeface(Typeface.MONOSPACE);
        webUrlView.setPadding(dp(12), dp(10), dp(12), dp(10));
        webUrlView.setBackground(ripple(roundBox(cPrimaryContainer, 0, 12), 12));
        webUrlView.setVisibility(View.GONE);
        webUrlView.setOnClickListener(v -> copyText("网页分享地址", webShareUrl, "已复制网页分享链接"));
        LinearLayout.LayoutParams wuLp = new LinearLayout.LayoutParams(MATCH, WRAP);
        wuLp.topMargin = dp(10);
        toolsCard.addView(webUrlView, wuLp);

        // Recent received files card
        LinearLayout recentCard = md3Card(cSurfaceContainerLow, 20, 16, 16, 16, 16);
        LinearLayout.LayoutParams rcLp = new LinearLayout.LayoutParams(MATCH, WRAP);
        rcLp.topMargin = dp(14);
        root.addView(recentCard, rcLp);

        LinearLayout recHead = new LinearLayout(this);
        recHead.setOrientation(LinearLayout.HORIZONTAL);
        recHead.setGravity(Gravity.CENTER_VERTICAL);
        recHead.addView(label("最近收到", 15, cOnSurface, true),
                new LinearLayout.LayoutParams(0, WRAP, 1f));
        recHead.addView(label("Download/Ferry", 11, cOnSurfaceVariant, true),
                new LinearLayout.LayoutParams(WRAP, WRAP));
        recentCard.addView(recHead, new LinearLayout.LayoutParams(MATCH, WRAP));

        recentListView = new LinearLayout(this);
        recentListView.setOrientation(LinearLayout.VERTICAL);
        LinearLayout.LayoutParams rlLp = new LinearLayout.LayoutParams(MATCH, WRAP);
        rlLp.topMargin = dp(10);
        recentCard.addView(recentListView, rlLp);
        renderRecentList();
    }

    // ================= Tab 2: Devices & Pairing =================

    private void buildPairPanel(LinearLayout root) {
        LinearLayout idCard = md3Card(cSurfaceContainerLow, 20, 16, 16, 16, 16);
        root.addView(idCard, new LinearLayout.LayoutParams(MATCH, WRAP));

        idCard.addView(label("本机身份", 15, cOnSurface, true),
                new LinearLayout.LayoutParams(WRAP, WRAP));

        LinearLayout.LayoutParams nlLp = new LinearLayout.LayoutParams(WRAP, WRAP);
        nlLp.topMargin = dp(10);
        idCard.addView(label("显示名称", 12, cOnSurfaceVariant, false), nlLp);

        nameField = md3Input("对端看到的设备名称");
        nameField.setText(deviceName);
        nameField.addTextChangedListener(new TextWatcher() {
            @Override public void beforeTextChanged(CharSequence s, int st, int c, int a) {}
            @Override public void onTextChanged(CharSequence s, int st, int b, int c) {}
            @Override public void afterTextChanged(Editable s) {
                String n = s.toString().trim();
                if (!n.isEmpty()) {
                    getPreferences(MODE_PRIVATE).edit().putString("deviceName", n).apply();
                }
            }
        });
        LinearLayout.LayoutParams nfLp = new LinearLayout.LayoutParams(MATCH, dp(48));
        nfLp.topMargin = dp(6);
        idCard.addView(nameField, nfLp);

        LinearLayout.LayoutParams flLp = new LinearLayout.LayoutParams(WRAP, WRAP);
        flLp.topMargin = dp(12);
        idCard.addView(label("设备 ID（点击复制）", 12, cOnSurfaceVariant, false), flLp);

        pairFullIdView = label("加载中…", 11, cOnSurface, false);
        pairFullIdView.setTypeface(Typeface.MONOSPACE);
        pairFullIdView.setPadding(dp(12), dp(10), dp(12), dp(10));
        pairFullIdView.setBackground(ripple(roundBox(cSurfaceContainerHigh, 0, 12), 12));
        pairFullIdView.setOnClickListener(v -> copyText("设备 ID", fullDeviceId, "已复制完整设备 ID"));
        LinearLayout.LayoutParams fidLp = new LinearLayout.LayoutParams(MATCH, WRAP);
        fidLp.topMargin = dp(6);
        idCard.addView(pairFullIdView, fidLp);

        Button copyUriBtn = pillFilledBtn("复制本机配对 URI", cSecondaryContainer, cOnSecondaryContainer);
        copyUriBtn.setOnClickListener(v -> showAndCopyPairUri());
        LinearLayout.LayoutParams cuLp = new LinearLayout.LayoutParams(MATCH, dp(44));
        cuLp.topMargin = dp(12);
        idCard.addView(copyUriBtn, cuLp);

        // 6-digit PAKE card
        LinearLayout pakeCard = md3Card(cSurfaceContainerLow, 20, 16, 16, 16, 16);
        LinearLayout.LayoutParams pcLp = new LinearLayout.LayoutParams(MATCH, WRAP);
        pcLp.topMargin = dp(14);
        root.addView(pakeCard, pcLp);

        pakeCard.addView(label("6 位一次性配对码", 15, cOnSurface, true),
                new LinearLayout.LayoutParams(WRAP, WRAP));
        pakeCodeField = md3Input("输入 6 位数字配对码");
        pakeCodeField.setInputType(InputType.TYPE_CLASS_NUMBER);
        LinearLayout.LayoutParams pcfLp = new LinearLayout.LayoutParams(MATCH, dp(48));
        pcfLp.topMargin = dp(10);
        pakeCard.addView(pakeCodeField, pcfLp);

        pakePeerField = md3Input("对方设备 ID（点“等待配对”时留空）");
        LinearLayout.LayoutParams ppfLp = new LinearLayout.LayoutParams(MATCH, dp(48));
        ppfLp.topMargin = dp(8);
        pakeCard.addView(pakePeerField, ppfLp);

        LinearLayout pakeBtns = new LinearLayout(this);
        pakeBtns.setOrientation(LinearLayout.HORIZONTAL);
        Button waitBtn = pillOutlineBtn("等待配对");
        waitBtn.setOnClickListener(v -> startPake(true));
        Button doBtn = pillFilledBtn("开始配对", cPrimary, cOnPrimary);
        doBtn.setOnClickListener(v -> startPake(false));
        pakeBtns.addView(waitBtn, new LinearLayout.LayoutParams(0, dp(44), 1f));
        LinearLayout.LayoutParams dbLp = new LinearLayout.LayoutParams(0, dp(44), 1f);
        dbLp.leftMargin = dp(10);
        pakeBtns.addView(doBtn, dbLp);
        LinearLayout.LayoutParams pbLp = new LinearLayout.LayoutParams(MATCH, WRAP);
        pbLp.topMargin = dp(12);
        pakeCard.addView(pakeBtns, pbLp);

        pakeInfoView = label("配对完成后，对方设备会自动出现在「发送」页。", 12, cOnSurfaceVariant, false);
        LinearLayout.LayoutParams piLp = new LinearLayout.LayoutParams(MATCH, WRAP);
        piLp.topMargin = dp(8);
        pakeCard.addView(pakeInfoView, piLp);

        // URI / file import card
        LinearLayout impCard = md3Card(cSurfaceContainerLow, 20, 16, 16, 16, 16);
        LinearLayout.LayoutParams icLp = new LinearLayout.LayoutParams(MATCH, WRAP);
        icLp.topMargin = dp(14);
        root.addView(impCard, icLp);

        impCard.addView(label("通过 URI 或文件导入", 15, cOnSurface, true),
                new LinearLayout.LayoutParams(WRAP, WRAP));
        pairUriField = md3Input("粘贴 ftpair://… 凭证");
        LinearLayout.LayoutParams puLp = new LinearLayout.LayoutParams(MATCH, dp(48));
        puLp.topMargin = dp(10);
        impCard.addView(pairUriField, puLp);

        LinearLayout impBtns = new LinearLayout(this);
        impBtns.setOrientation(LinearLayout.HORIZONTAL);
        Button impUriBtn = pillOutlineBtn("导入 URI");
        impUriBtn.setOnClickListener(v -> importPairUri());
        Button impFileBtn = pillOutlineBtn("选择配对文件");
        impFileBtn.setOnClickListener(v -> pickPairFile());
        impBtns.addView(impUriBtn, new LinearLayout.LayoutParams(0, dp(44), 1f));
        LinearLayout.LayoutParams ifLp = new LinearLayout.LayoutParams(0, dp(44), 1f);
        ifLp.leftMargin = dp(10);
        impBtns.addView(impFileBtn, ifLp);
        LinearLayout.LayoutParams ibLp = new LinearLayout.LayoutParams(MATCH, WRAP);
        ibLp.topMargin = dp(10);
        impCard.addView(impBtns, ibLp);
    }

    // ================= Receive Control (Unified) =================

    private void toggleReceive() {
        if (recvRunning) {
            stopAllReceive();
        } else {
            startAllReceive();
        }
    }

    private void startAllReceive() {
        recvToggleBtn.setEnabled(false);
        final String name = currentName();
        new Thread(() -> {
            String lanAddr = "";
            String autoErr = "";
            try {
                File inbox = inboxDir();
                lanAddr = Mobile.startReceiver(name, inbox.getAbsolutePath(), lanHint());
            } catch (Exception e) {
                Log.e(TAG, "startReceiver", e);
            }
            try {
                File inbox = inboxDir();
                Mobile.startAutoReceive(name, inbox.getAbsolutePath(), SIGNAL_URL,
                        keyFile().getAbsolutePath(), pairedFile().getAbsolutePath(), lanHint(), "");
            } catch (Exception e) {
                Log.e(TAG, "startAutoReceive", e);
                autoErr = e.getMessage();
            }
            final String addr = lanAddr;
            final String err = autoErr;
            ui.post(() -> {
                recvToggleBtn.setEnabled(true);
                if (addr.isEmpty() && !err.isEmpty()) {
                    toast("启动接收失败：" + err);
                    return;
                }
                recvRunning = true;
                recvEverStarted = true;
                lanPort = extractPort(addr);
                acquireMulticastLock();
                startKeepAlive(lanPort.isEmpty() ? "正在等待接收文件" : "接收中 · 端口 " + lanPort);
                refreshReceiveUi();
                ui.removeCallbacks(pollTask);
                ui.postDelayed(pollTask, 1000);
            });
        }).start();
    }

    private void stopAllReceive() {
        recvToggleBtn.setEnabled(false);
        ui.removeCallbacks(pollTask);
        new Thread(() -> {
            try {
                Mobile.stopReceiver();
                Mobile.stopAutoReceive();
                Mobile.stopRelayReceiver();
                Mobile.stopPunchReceiver();
            } catch (Exception e) {
                Log.e(TAG, "stopAllReceive", e);
            }
            pollReceivedOnce();
            ui.post(() -> {
                recvToggleBtn.setEnabled(true);
                recvRunning = false;
                lanPort = "";
                releaseMulticastLock();
                if (!webRunning) {
                    stopKeepAlive();
                }
                refreshReceiveUi();
                toast("已停止接收");
            });
        }).start();
    }

    private void toggleWebShare() {
        if (webRunning) {
            webToggleBtn.setEnabled(false);
            new Thread(() -> {
                try {
                    Mobile.stopWebShare();
                } catch (Exception e) {
                    Log.w(TAG, "stopWebShare", e);
                }
                ui.post(() -> {
                    webToggleBtn.setEnabled(true);
                    webRunning = false;
                    webShareUrl = "";
                    if (!recvRunning) {
                        stopKeepAlive();
                    }
                    refreshReceiveUi();
                });
            }).start();
            return;
        }
        webToggleBtn.setEnabled(false);
        new Thread(() -> {
            try {
                String info = Mobile.startWebShare(webShareDir().getAbsolutePath(), 0L, lanHint());
                String url = info;
                try {
                    url = new JSONObject(info).optString("url", info);
                } catch (Exception ignored) {
                }
                final String shown = url;
                ui.post(() -> {
                    webToggleBtn.setEnabled(true);
                    webRunning = true;
                    webShareUrl = shown;
                    startKeepAlive("网页分享中");
                    refreshReceiveUi();
                });
            } catch (Exception e) {
                Log.e(TAG, "startWebShare", e);
                ui.post(() -> {
                    webToggleBtn.setEnabled(true);
                    toast("网页分享启动失败：" + e.getMessage());
                });
            }
        }).start();
    }

    private void refreshReceiveUi() {
        boolean active = recvRunning || webRunning;
        if (topStateText != null) {
            topStateText.setText(recvRunning ? "接收中" : (webRunning ? "网页分享中" : (recvEverStarted ? "已暂停" : "未开启")));
            topStateText.setTextColor(active ? cOnPrimaryContainer : cOnSurfaceVariant);
        }
        if (topDot != null) {
            topDot.setBackground(oval(active ? cPrimary : cOnSurfaceVariant));
        }
        if (topChip != null) {
            topChip.setBackground(ripple(roundBox(active ? cPrimaryContainer : cSurfaceContainerHigh, 0, 99), 99));
        }
        if (active) {
            startDotPulse();
        } else {
            stopDotPulse();
        }

        if (recvHeroCard != null) {
            recvHeroCard.setBackground(roundBox(recvRunning ? cPrimaryContainer : cSurfaceContainerLow, 0, 24));
        }
        if (recvHeroTitle != null) {
            recvHeroTitle.setText(recvRunning ? "正在等待接收文件" : "准备接收文件");
            recvHeroTitle.setTextColor(recvRunning ? cOnPrimaryContainer : cOnSurface);
        }
        if (recvHeroSub != null) {
            if (recvRunning) {
                String p = lanPort.isEmpty() ? "—" : lanPort;
                recvHeroSub.setText("局域网端口 " + p + " · 远程自动选路已连接\n收到后自动存入系统「下载 / Ferry」");
                recvHeroSub.setTextColor(cOnPrimaryContainer);
            } else {
                recvHeroSub.setText("点击下方按钮后同时开启局域网与远程自动选路，收到文件自动保存到系统「下载 / Ferry」");
                recvHeroSub.setTextColor(cOnSurfaceVariant);
            }
        }
        if (recvToggleBtn != null) {
            recvToggleBtn.setText(recvRunning ? "停止接收" : "开启接收");
            applyFilledStyle(recvToggleBtn, recvRunning ? cError : cPrimary, recvRunning ? cOnError : cOnPrimary);
        }
        if (webToggleBtn != null) {
            webToggleBtn.setText(webRunning ? "关闭网页分享" : "开启网页分享");
            applyFilledStyle(webToggleBtn,
                    webRunning ? cErrorContainer : cSecondaryContainer,
                    webRunning ? cOnErrorContainer : cOnSecondaryContainer);
        }
        if (webUrlView != null) {
            if (webRunning && !webShareUrl.isEmpty()) {
                webUrlView.setText("点击复制（同一 Wi-Fi 浏览器打开）：\n" + webShareUrl);
                webUrlView.setVisibility(View.VISIBLE);
            } else {
                webUrlView.setVisibility(View.GONE);
            }
        }
    }

    private static String extractPort(String addr) {
        if (addr == null) {
            return "";
        }
        int i = addr.lastIndexOf(':');
        return i >= 0 ? addr.substring(i + 1).trim() : "";
    }

    // ================= Receive Poll -> Public Download/ft =================

    private void pollReceivedOnce() {
        ioPool.execute(() -> {
            try {
                String raw = Mobile.pollReceived();
                if (raw == null || raw.equals("[]") || raw.trim().isEmpty()) {
                    return;
                }
                JSONArray arr = new JSONArray(raw);
                for (int i = 0; i < arr.length(); i++) {
                    JSONObject o = arr.getJSONObject(i);
                    String fileName = o.optString("file", "");
                    String fullPath = o.optString("full_path", "");
                    String via = o.optString("via", "");
                    long bytes = o.optLong("bytes", 0);
                    long ts = o.optLong("ts", System.currentTimeMillis());
                    if (fileName.isEmpty() || fullPath.isEmpty()) {
                        continue;
                    }
                    File src = new File(fullPath);
                    if (!src.isFile()) {
                        continue;
                    }
                    exportStagedFile(src, fileName, via, bytes, ts);
                }
            } catch (Exception e) {
                Log.w(TAG, "pollReceivedOnce", e);
            }
        });
    }

    /** Moves any non-.ftpart files left in inboxDir after a process restart. */
    private void sweepLeftoverInbox() {
        ioPool.execute(() -> {
            File[] files = inboxDir().listFiles();
            if (files == null) {
                return;
            }
            for (File f : files) {
                if (f.isFile() && !f.getName().endsWith(".ftpart")) {
                    exportStagedFile(f, f.getName(), "", f.length(), f.lastModified());
                }
            }
        });
    }

    private void exportStagedFile(File src, String fileName, String via, long bytes, long ts) {
        String mime = guessMime(fileName);
        String savedUri = saveToPublicDownloads(src, fileName, mime);
        if (savedUri == null) {
            return;
        }
        if (!src.delete()) {
            Log.w(TAG, "could not delete staged file: " + src);
        }
        final ReceivedEntry entry = new ReceivedEntry(fileName, savedUri, mime, via, bytes, ts);
        ui.post(() -> {
            recentEntries.add(0, entry);
            while (recentEntries.size() > 20) {
                recentEntries.remove(recentEntries.size() - 1);
            }
            persistRecentState();
            renderRecentList();
            String viaTag = pathLabel(via);
            toast("已保存到「下载/Ferry」" + (viaTag.isEmpty() ? "" : "（" + viaTag + "）") + "：" + fileName);
        });
    }

    private String saveToPublicDownloads(File src, String fileName, String mime) {
        if (Build.VERSION.SDK_INT >= 29) {
            ContentValues cv = new ContentValues();
            cv.put(MediaStore.Downloads.DISPLAY_NAME, fileName);
            cv.put(MediaStore.Downloads.MIME_TYPE, mime);
            cv.put(MediaStore.Downloads.RELATIVE_PATH, Environment.DIRECTORY_DOWNLOADS + "/Ferry");
            cv.put(MediaStore.Downloads.IS_PENDING, 1);
            Uri uri = getContentResolver().insert(MediaStore.Downloads.EXTERNAL_CONTENT_URI, cv);
            if (uri == null) {
                Log.e(TAG, "MediaStore.Downloads insert returned null");
                return null;
            }
            try (InputStream in = new FileInputStream(src);
                 OutputStream out = getContentResolver().openOutputStream(uri)) {
                if (out == null) {
                    throw new Exception("openOutputStream null");
                }
                copyStream(in, out);
                ContentValues done = new ContentValues();
                done.put(MediaStore.Downloads.IS_PENDING, 0);
                getContentResolver().update(uri, done, null, null);
                return uri.toString();
            } catch (Exception e) {
                Log.e(TAG, "saveToPublicDownloads API29+", e);
                try {
                    getContentResolver().delete(uri, null, null);
                } catch (Exception ignored) {
                }
                return null;
            }
        }
        try {
            File dl = Environment.getExternalStoragePublicDirectory(Environment.DIRECTORY_DOWNLOADS);
            File dir = new File(dl, "Ferry");
            if (!dir.exists() && !dir.mkdirs()) {
                return null;
            }
            File dst = uniqueDestFile(dir, fileName);
            try (InputStream in = new FileInputStream(src);
                 OutputStream out = new FileOutputStream(dst)) {
                copyStream(in, out);
            }
            return Uri.fromFile(dst).toString();
        } catch (Exception e) {
            Log.e(TAG, "saveToPublicDownloads legacy", e);
            return null;
        }
    }

    private static File uniqueDestFile(File dir, String name) {
        File f = new File(dir, name);
        if (!f.exists()) {
            return f;
        }
        int dot = name.lastIndexOf('.');
        String base = dot > 0 ? name.substring(0, dot) : name;
        String ext = dot > 0 ? name.substring(dot) : "";
        for (int i = 1; i < 1000; i++) {
            File cand = new File(dir, base + " (" + i + ")" + ext);
            if (!cand.exists()) {
                return cand;
            }
        }
        return new File(dir, System.currentTimeMillis() + "-" + name);
    }

    private static String guessMime(String name) {
        int dot = name.lastIndexOf('.');
        if (dot >= 0 && dot < name.length() - 1) {
            String ext = name.substring(dot + 1).toLowerCase(Locale.US);
            String m = MimeTypeMap.getSingleton().getMimeTypeFromExtension(ext);
            if (m != null && !m.isEmpty()) {
                return m;
            }
        }
        return "application/octet-stream";
    }

    private static void copyStream(InputStream in, OutputStream out) throws Exception {
        byte[] buf = new byte[65536];
        int n;
        while ((n = in.read(buf)) > 0) {
            out.write(buf, 0, n);
        }
    }

    private void loadRecentState() {
        ioPool.execute(() -> {
            File rf = recentFile();
            if (!rf.isFile()) {
                return;
            }
            try (InputStream in = new FileInputStream(rf)) {
                ByteArrayOutputStream bos = new ByteArrayOutputStream();
                copyStream(in, bos);
                JSONArray arr = new JSONArray(bos.toString("UTF-8"));
                final List<ReceivedEntry> loaded = new ArrayList<>();
                for (int i = 0; i < arr.length(); i++) {
                    JSONObject o = arr.getJSONObject(i);
                    loaded.add(new ReceivedEntry(
                            o.optString("name", ""),
                            o.optString("uri", ""),
                            o.optString("mime", "application/octet-stream"),
                            o.optString("via", ""),
                            o.optLong("bytes", 0),
                            o.optLong("ts", 0)));
                }
                ui.post(() -> {
                    recentEntries.clear();
                    recentEntries.addAll(loaded);
                    renderRecentList();
                });
            } catch (Exception e) {
                Log.w(TAG, "loadRecentState", e);
            }
        });
    }

    private void persistRecentState() {
        final List<ReceivedEntry> snap = new ArrayList<>(recentEntries);
        ioPool.execute(() -> {
            try {
                JSONArray arr = new JSONArray();
                for (ReceivedEntry r : snap) {
                    JSONObject o = new JSONObject();
                    o.put("name", r.name);
                    o.put("uri", r.uri);
                    o.put("mime", r.mime);
                    o.put("via", r.via);
                    o.put("bytes", r.bytes);
                    o.put("ts", r.ts);
                    arr.put(o);
                }
                try (OutputStream out = new FileOutputStream(recentFile())) {
                    out.write(arr.toString().getBytes("UTF-8"));
                }
            } catch (Exception e) {
                Log.w(TAG, "persistRecentState", e);
            }
        });
    }

    private void renderRecentList() {
        if (recentListView == null) {
            return;
        }
        recentListView.removeAllViews();
        if (recentEntries.isEmpty()) {
            TextView empty = label("暂未收到文件。对方发送成功后，文件会自动保存在系统「下载 / Ferry」文件夹。",
                    12, cOnSurfaceVariant, false);
            recentListView.addView(empty, new LinearLayout.LayoutParams(MATCH, WRAP));
            return;
        }
        SimpleDateFormat sdf = new SimpleDateFormat("HH:mm:ss", Locale.US);
        for (int i = 0; i < recentEntries.size(); i++) {
            final ReceivedEntry r = recentEntries.get(i);
            LinearLayout row = new LinearLayout(this);
            row.setOrientation(LinearLayout.HORIZONTAL);
            row.setGravity(Gravity.CENTER_VERTICAL);
            row.setPadding(dp(12), dp(10), dp(12), dp(10));
            row.setBackground(ripple(roundBox(cSurface, 0, 14), 14));
            row.setOnClickListener(v -> openReceivedEntry(r));

            LinearLayout col = new LinearLayout(this);
            col.setOrientation(LinearLayout.VERTICAL);
            TextView nm = label(r.name, 14, cOnSurface, true);
            nm.setSingleLine(true);
            nm.setEllipsize(TextUtils.TruncateAt.MIDDLE);
            col.addView(nm, new LinearLayout.LayoutParams(MATCH, WRAP));

            String meta = fmtBytes(r.bytes) + (r.ts > 0 ? " · " + sdf.format(new Date(r.ts)) : "");
            TextView mt = label(meta, 11, cOnSurfaceVariant, false);
            LinearLayout.LayoutParams mtLp = new LinearLayout.LayoutParams(WRAP, WRAP);
            mtLp.topMargin = dp(2);
            col.addView(mt, mtLp);
            row.addView(col, new LinearLayout.LayoutParams(0, WRAP, 1f));

            String vLbl = pathLabel(r.via);
            if (!vLbl.isEmpty()) {
                TextView badge = pathBadgeView(r.via);
                LinearLayout.LayoutParams bLp = new LinearLayout.LayoutParams(WRAP, WRAP);
                bLp.leftMargin = dp(8);
                row.addView(badge, bLp);
            }

            LinearLayout.LayoutParams rowLp = new LinearLayout.LayoutParams(MATCH, WRAP);
            if (i > 0) {
                rowLp.topMargin = dp(6);
            }
            recentListView.addView(row, rowLp);
        }
    }

    private void openReceivedEntry(ReceivedEntry r) {
        try {
            Intent intent = new Intent(Intent.ACTION_VIEW);
            intent.setDataAndType(Uri.parse(r.uri), r.mime);
            intent.addFlags(Intent.FLAG_GRANT_READ_URI_PERMISSION | Intent.FLAG_ACTIVITY_NEW_TASK);
            startActivity(intent);
        } catch (Exception e) {
            toast("文件已保存在「下载 / Ferry / " + r.name + "」");
        }
    }

    // ================= Send Flows (Files & Clipboard) =================

    private void handleShareIntent(Intent intent) {
        if (intent == null) {
            return;
        }
        String action = intent.getAction();
        if (!Intent.ACTION_SEND.equals(action) && !Intent.ACTION_SEND_MULTIPLE.equals(action)) {
            return;
        }
        List<Uri> uris = new ArrayList<>();
        if (intent.getClipData() != null) {
            for (int i = 0; i < intent.getClipData().getItemCount(); i++) {
                uris.add(intent.getClipData().getItemAt(i).getUri());
            }
        } else if (intent.getParcelableExtra(Intent.EXTRA_STREAM) instanceof Uri) {
            uris.add(intent.getParcelableExtra(Intent.EXTRA_STREAM));
        }
        if (uris.isEmpty()) {
            return;
        }
        sharedUris = uris;
        selectTab(0);
        if (shareBannerView != null) {
            shareBannerView.setText("已从其他应用带入 " + uris.size() + " 个文件 · 点上方按钮直接发送");
            shareBannerView.setVisibility(View.VISIBLE);
        }
    }

    private void pickFiles() {
        final String target = currentTarget();
        if (!isAutoTarget(target) && !isLanTarget(target)) {
            toast("请先选择接收设备，或填写 64 位设备 ID / IP:端口");
            return;
        }
        saveLastPeer(target);
        if (sharedUris != null && !sharedUris.isEmpty()) {
            List<Uri> uris = sharedUris;
            sharedUris = null;
            if (shareBannerView != null) {
                shareBannerView.setVisibility(View.GONE);
            }
            sendUriBatch(uris, currentName(), target);
            return;
        }
        Intent i = new Intent(Intent.ACTION_OPEN_DOCUMENT);
        i.addCategory(Intent.CATEGORY_OPENABLE);
        i.setType("*/*");
        i.putExtra(Intent.EXTRA_ALLOW_MULTIPLE, true);
        startActivityForResult(i, PICK_FILES);
    }

    private void sendClipboard() {
        final String target = currentTarget();
        if (!isAutoTarget(target) && !isLanTarget(target)) {
            toast("请先选择接收设备，或填写 64 位设备 ID / IP:端口");
            return;
        }
        ClipboardManager cm = (ClipboardManager) getSystemService(CLIPBOARD_SERVICE);
        if (cm == null || !cm.hasPrimaryClip() || cm.getPrimaryClip() == null
                || cm.getPrimaryClip().getItemCount() == 0) {
            toast("剪贴板里没有文本");
            return;
        }
        CharSequence cs = cm.getPrimaryClip().getItemAt(0).coerceToText(this);
        String text = cs == null ? "" : cs.toString();
        if (text.trim().isEmpty()) {
            toast("剪贴板文本为空");
            return;
        }
        saveLastPeer(target);
        final String name = currentName();
        final boolean auto = isAutoTarget(target);
        setSendBusy(true, auto ? "正在发送剪贴板文本（自动选路）…" : "正在通过局域网发送剪贴板文本…");
        new Thread(() -> {
            File dir = new File(getCacheDir(), "clip-" + System.nanoTime());
            dir.mkdirs();
            String ts = new SimpleDateFormat("HHmmss", Locale.US).format(new Date());
            File f = new File(dir, "clipboard-" + ts + ".txt");
            try (OutputStream out = new FileOutputStream(f)) {
                out.write(text.getBytes("UTF-8"));
                List<File> list = new ArrayList<>();
                list.add(f);
                sendPreparedFiles(list, dir, name, target, auto);
            } catch (Exception e) {
                Log.e(TAG, "sendClipboard", e);
                deleteTree(f, dir);
                onSendFailed(e.getMessage());
            }
        }).start();
    }

    private void sendUriBatch(final List<Uri> uris, final String name, final String target) {
        final boolean auto = isAutoTarget(target);
        setSendBusy(true, auto
                ? "正在发送 " + uris.size() + " 个文件（LAN / 打洞 / 中继自动竞速）…"
                : "正在通过局域网发送 " + uris.size() + " 个文件…");
        new Thread(() -> {
            File batchDir = new File(new File(getCacheDir(), "outgoing"), String.valueOf(System.nanoTime()));
            List<File> tempFiles = new ArrayList<>();
            try {
                for (int i = 0; i < uris.size(); i++) {
                    tempFiles.add(copyToCache(uris.get(i), new File(batchDir, String.valueOf(i))));
                }
                sendPreparedFiles(tempFiles, batchDir, name, target, auto);
            } catch (Exception e) {
                Log.e(TAG, "sendUriBatch", e);
                for (File tf : tempFiles) {
                    if (tf != null) {
                        deleteTree(tf, tf.getParentFile());
                    }
                }
                batchDir.delete();
                onSendFailed(e.getMessage());
            }
        }).start();
    }

    private void sendPreparedFiles(List<File> files, File rootTmp, String name, String target, boolean auto) {
        try {
            JSONArray paths = new JSONArray();
            for (File f : files) {
                paths.put(f.getAbsolutePath());
            }
            final String summary = auto
                    ? Mobile.sendAuto(SIGNAL_URL, keyFile().getAbsolutePath(),
                            pairedFile().getAbsolutePath(), target, name, paths.toString(), lanHint(), "")
                    : Mobile.sendFiles(target, name, paths.toString());
            onSendSucceeded(summary);
        } catch (Exception e) {
            Log.e(TAG, auto ? "sendAuto" : "sendLan", e);
            onSendFailed(e.getMessage());
        } finally {
            for (File f : files) {
                if (f != null) {
                    deleteTree(f, f.getParentFile());
                }
            }
            if (rootTmp != null) {
                rootTmp.delete();
            }
        }
    }

    private void setSendBusy(boolean busy, String msg) {
        if (sendPickBtn != null) {
            sendPickBtn.setEnabled(!busy);
        }
        if (sendClipBtn != null) {
            sendClipBtn.setEnabled(!busy);
        }
        if (busy) {
            startKeepAlive(msg);
            showSendBanner("", msg, false);
        }
    }

    private void onSendSucceeded(final String raw) {
        ui.post(() -> {
            setSendBusy(false, "");
            if (recvRunning || webRunning) {
                startKeepAlive(recvRunning ? "接收中" : "网页分享中");
            } else {
                stopKeepAlive();
            }
            try {
                JSONObject o = new JSONObject(raw);
                if (!o.optBoolean("ok", false)) {
                    showSendBanner("fail", "发送失败：" + o.optString("error", raw), true);
                    return;
                }
                String path = o.optString("path", "");
                JSONArray files = o.optJSONArray("files");
                int n = files != null ? files.length() : 0;
                long bytes = o.optLong("bytes", 0);
                long ms = o.optLong("duration_ms", 0);
                String text = "发送完成 · " + n + " 个文件 · " + fmtBytes(bytes) + " · " + ms + " ms";
                showSendBanner(path, text, false);
            } catch (Exception e) {
                showSendBanner("", raw, false);
            }
        });
    }

    private void onSendFailed(final String err) {
        ui.post(() -> {
            setSendBusy(false, "");
            if (recvRunning || webRunning) {
                startKeepAlive(recvRunning ? "接收中" : "网页分享中");
            } else {
                stopKeepAlive();
            }
            showSendBanner("fail", "发送失败：" + err, true);
        });
    }

    private void showSendBanner(String path, String msg, boolean isError) {
        if (sendResultCard == null || sendResultText == null || sendResultBadge == null) {
            return;
        }
        sendResultCard.setVisibility(View.VISIBLE);
        sendResultCard.setBackground(roundBox(isError ? cErrorContainer : cSurfaceContainerHigh, 0, 14));
        sendResultText.setText(msg);
        sendResultText.setTextColor(isError ? cOnErrorContainer : cOnSurface);

        if (isError) {
            sendResultBadge.setText("失败");
            sendResultBadge.setTextColor(cOnError);
            sendResultBadge.setBackground(roundBox(cError, 0, 99));
            sendResultBadge.setVisibility(View.VISIBLE);
        } else {
            String lbl = pathLabel(path);
            if (lbl.isEmpty()) {
                sendResultBadge.setVisibility(View.GONE);
            } else {
                stylePathBadge(sendResultBadge, path);
                sendResultBadge.setVisibility(View.VISIBLE);
            }
        }
    }

    @Override
    protected void onActivityResult(int requestCode, int resultCode, Intent data) {
        super.onActivityResult(requestCode, resultCode, data);
        if (resultCode != RESULT_OK || data == null) {
            return;
        }
        if (requestCode == PICK_PAIR_FILE) {
            if (data.getData() != null) {
                importPairFile(data.getData());
            }
            return;
        }
        if (requestCode != PICK_FILES) {
            return;
        }
        List<Uri> uris = new ArrayList<>();
        if (data.getClipData() != null) {
            for (int i = 0; i < data.getClipData().getItemCount(); i++) {
                uris.add(data.getClipData().getItemAt(i).getUri());
            }
        } else if (data.getData() != null) {
            uris.add(data.getData());
        }
        if (!uris.isEmpty()) {
            sendUriBatch(uris, currentName(), currentTarget());
        }
    }

    // ================= Devices, Discovery & Pairing =================

    private void loadDeviceId() {
        ioPool.execute(() -> {
            try {
                String id = Mobile.deviceIDFor(keyFile().getAbsolutePath());
                final String full = id == null ? "" : id;
                final String shown = full.length() > 12 ? full.substring(0, 12) : full;
                ui.post(() -> {
                    fullDeviceId = full;
                    if (topIdChip != null && !shown.isEmpty()) {
                        topIdChip.setText("ID " + shown);
                    }
                    if (pairFullIdView != null && !full.isEmpty()) {
                        pairFullIdView.setText(full);
                    }
                });
            } catch (Exception e) {
                Log.w(TAG, "loadDeviceId", e);
            }
        });
    }

    private void loadPairedPeers() {
        ioPool.execute(() -> {
            try {
                String json = Mobile.pairedPeers(pairedFile().getAbsolutePath());
                JSONArray arr = new JSONArray(json);
                final List<PeerEntry> entries = new ArrayList<>();
                for (int i = 0; i < arr.length(); i++) {
                    JSONObject o = arr.getJSONObject(i);
                    String id = o.optString("id", "").trim();
                    if (id.isEmpty()) {
                        continue;
                    }
                    String name = o.optString("name", "").trim();
                    if (name.isEmpty()) {
                        name = "已配对设备";
                    }
                    String shortId = id.length() > 12 ? id.substring(0, 12) + "…" : id;
                    entries.add(new PeerEntry(name, "已配对 · 自动选路 · " + shortId, id));
                }
                ui.post(() -> {
                    pairedEntries.clear();
                    pairedEntries.addAll(entries);
                    if (peerField != null && currentTarget().isEmpty() && !entries.isEmpty()) {
                        peerField.setText(entries.get(0).value);
                    }
                    if (deviceList != null) {
                        deviceList.render(pairedEntries, lanEntries, lanNote);
                    }
                    updateTargetSummary();
                });
            } catch (Exception e) {
                Log.w(TAG, "loadPairedPeers", e);
            }
        });
    }

    private void discover() {
        discoverBtn.setEnabled(false);
        lanNote = "正在搜索同一局域网设备…";
        deviceList.render(pairedEntries, lanEntries, lanNote);
        final String name = currentName();
        new Thread(() -> {
            try {
                String json = Mobile.discover(name, 3000, lanHint());
                JSONArray arr = new JSONArray(json);
                final List<PeerEntry> found = new ArrayList<>();
                for (int i = 0; i < arr.length(); i++) {
                    JSONObject o = arr.getJSONObject(i);
                    String addr = o.optString("Addr", "").trim();
                    if (addr.isEmpty()) {
                        continue;
                    }
                    String dn = o.optString("Name", "").trim();
                    if (dn.isEmpty()) {
                        dn = "局域网设备";
                    }
                    found.add(new PeerEntry(dn, "局域网直连 · " + addr, addr));
                }
                ui.post(() -> {
                    lanEntries.clear();
                    lanEntries.addAll(found);
                    lanNote = found.isEmpty() ? "未发现局域网在线设备，可直接使用已配对设备。" : "";
                    if (!found.isEmpty() && currentTarget().isEmpty()) {
                        peerField.setText(found.get(0).value);
                    }
                    deviceList.render(pairedEntries, lanEntries, lanNote);
                    updateTargetSummary();
                    discoverBtn.setEnabled(true);
                });
            } catch (Exception e) {
                ui.post(() -> {
                    lanNote = "搜索失败：" + e.getMessage();
                    deviceList.render(pairedEntries, lanEntries, lanNote);
                    discoverBtn.setEnabled(true);
                });
            }
        }).start();
    }

    private void showAndCopyPairUri() {
        try {
            String uri = Mobile.pairURIFor(currentName(), keyFile().getAbsolutePath());
            if (pairUriField != null) {
                pairUriField.setText(uri);
            }
            copyText("配对 URI", uri, "已复制本机配对 URI");
        } catch (Exception e) {
            toast("生成配对 URI 失败：" + e.getMessage());
        }
    }

    private void importPairUri() {
        String uri = pairUriField.getText().toString().trim();
        if (uri.isEmpty()) {
            toast("先粘贴对方 ftpair:// URI");
            return;
        }
        confirmImportPairUri(uri);
    }

    private void pickPairFile() {
        Intent i = new Intent(Intent.ACTION_OPEN_DOCUMENT);
        i.addCategory(Intent.CATEGORY_OPENABLE);
        i.setType("text/plain");
        startActivityForResult(i, PICK_PAIR_FILE);
    }

    private void importPairFile(Uri uri) {
        try {
            ByteArrayOutputStream buf = new ByteArrayOutputStream();
            try (InputStream in = getContentResolver().openInputStream(uri)) {
                if (in == null) {
                    toast("无法读取配对文件");
                    return;
                }
                byte[] b = new byte[4096];
                int n;
                while ((n = in.read(b)) > 0) {
                    buf.write(b, 0, n);
                    if (buf.size() > 16384) {
                        toast("配对文件过大");
                        return;
                    }
                }
            }
            String text = buf.toString("UTF-8").trim();
            if (text.isEmpty()) {
                toast("配对文件为空");
                return;
            }
            confirmImportPairUri(text);
        } catch (Exception e) {
            toast("读取配对文件失败：" + e.getMessage());
        }
    }

    private void confirmImportPairUri(final String uri) {
        new AlertDialog.Builder(this)
                .setTitle("信任这台设备？")
                .setMessage("设备 ID：" + shortPeerId(uri) + "\n\n导入后该设备可向你发送文件。")
                .setNegativeButton("取消", null)
                .setPositiveButton("导入并信任", (d, w) -> {
                    try {
                        Mobile.pairImport(uri, pairedFile().getAbsolutePath());
                        toast("已导入配对设备");
                        loadPairedPeers();
                    } catch (Exception e) {
                        toast("导入失败：" + e.getMessage());
                    }
                })
                .show();
    }

    private void startPake(boolean wait) {
        String code = pakeCodeField.getText().toString().trim();
        if (code.length() != 6) {
            toast("请输入 6 位数字配对码");
            return;
        }
        String peer = pakePeerField.getText().toString().trim();
        if (!wait && peer.isEmpty()) {
            toast("请输入对方设备 ID，或点“等待配对”");
            return;
        }
        pakeInfoView.setText(wait ? "正在等待对方输入相同配对码…" : "正在协商信任密钥…");
        new Thread(() -> {
            try {
                String res = wait
                        ? Mobile.waitPairCode("", keyFile().getAbsolutePath(), pairedFile().getAbsolutePath(), code, 90000)
                        : Mobile.pairWithCode("", keyFile().getAbsolutePath(), pairedFile().getAbsolutePath(), code, peer);
                ui.post(() -> {
                    pakeInfoView.setText("配对成功：" + res);
                    loadPairedPeers();
                });
            } catch (Exception e) {
                ui.post(() -> pakeInfoView.setText("配对失败：" + e.getMessage()));
            }
        }).start();
    }

    private void updateTargetSummary() {
        if (targetTitleView == null || targetSubView == null) {
            return;
        }
        String t = currentTarget();
        if (t.isEmpty()) {
            targetTitleView.setText("尚未选择接收设备");
            targetSubView.setText("在下方列表选一台设备，或手动填入 ID / IP:端口");
            return;
        }
        for (PeerEntry e : pairedEntries) {
            if (e.value.equals(t)) {
                targetTitleView.setText(e.name);
                targetSubView.setText(e.sub);
                return;
            }
        }
        for (PeerEntry e : lanEntries) {
            if (e.value.equals(t)) {
                targetTitleView.setText(e.name);
                targetSubView.setText(e.sub);
                return;
            }
        }
        if (isAutoTarget(t)) {
            targetTitleView.setText("设备 " + t.substring(0, 12) + "…");
            targetSubView.setText("将尝试 局域网 / 打洞 / 中继 自动选路");
        } else {
            targetTitleView.setText(t);
            targetSubView.setText(isLanTarget(t) ? "局域网直连地址" : "格式需为 64 位设备 ID 或 IP:端口");
        }
    }

    private void importBootstrapPairs() {
        File boot = new File(getFilesDir(), "pair.import");
        if (boot.isFile()) {
            try (java.io.BufferedReader r = new java.io.BufferedReader(new java.io.FileReader(boot))) {
                String line;
                while ((line = r.readLine()) != null) {
                    line = line.trim();
                    if (line.startsWith("ftpair://")) {
                        Mobile.pairImport(line, pairedFile().getAbsolutePath());
                    }
                }
                boot.delete();
            } catch (Exception e) {
                Log.w(TAG, "importBootstrapPairs", e);
            }
        }
        if (pairedFile().isFile()) {
            copyForDebug(pairedFile());
        }
    }

    private void copyForDebug(File src) {
        try (InputStream in = new FileInputStream(src);
             OutputStream out = new FileOutputStream(new File(getFilesDir(), src.getName() + ".debug"))) {
            copyStream(in, out);
        } catch (Exception e) {
            Log.w(TAG, "copyForDebug", e);
        }
    }

    private void applyDebugPeerId() {
        File f = new File(getFilesDir(), "peer.id");
        if (!f.isFile()) {
            return;
        }
        try (InputStream in = new FileInputStream(f)) {
            ByteArrayOutputStream bos = new ByteArrayOutputStream();
            copyStream(in, bos);
            String id = bos.toString("UTF-8").trim();
            if (!id.isEmpty() && peerField != null) {
                peerField.setText(id);
            }
        } catch (Exception e) {
            Log.w(TAG, "applyDebugPeerId", e);
        }
    }

    private void prefillLastPeer() {
        String last = getPreferences(MODE_PRIVATE).getString("lastPeer", "");
        if (!last.isEmpty() && peerField != null && currentTarget().isEmpty()) {
            peerField.setText(last);
            updateTargetSummary();
        }
    }

    private void saveLastPeer(String peer) {
        getPreferences(MODE_PRIVATE).edit().putString("lastPeer", peer).apply();
    }

    private String currentTarget() {
        return peerField == null ? "" : peerField.getText().toString().trim();
    }

    private String loadSavedName() {
        SharedPreferences sp = getPreferences(MODE_PRIVATE);
        String saved = sp.getString("deviceName", "").trim();
        return saved.isEmpty() ? defaultName() : saved;
    }

    private String defaultName() {
        String model = Build.MODEL == null ? "phone" : Build.MODEL;
        String s = model.replaceAll("[^A-Za-z0-9]+", "-");
        if (s.isEmpty() || s.equals("-")) {
            s = "phone";
        }
        if (s.length() > 24) {
            s = s.substring(0, 24);
        }
        return "android-" + s;
    }

    private String currentName() {
        if (nameField == null) {
            return deviceName;
        }
        String n = nameField.getText().toString().trim();
        return n.isEmpty() ? deviceName : n;
    }

    // ================= Device List View =================

    private static class PeerEntry {
        final String name;
        final String sub;
        final String value;

        PeerEntry(String name, String sub, String value) {
            this.name = name;
            this.sub = sub;
            this.value = value;
        }
    }

    private static class ReceivedEntry {
        final String name;
        final String uri;
        final String mime;
        final String via;
        final long bytes;
        final long ts;

        ReceivedEntry(String name, String uri, String mime, String via, long bytes, long ts) {
            this.name = name;
            this.uri = uri;
            this.mime = mime;
            this.via = via;
            this.bytes = bytes;
            this.ts = ts;
        }
    }

    private class DeviceList extends LinearLayout {
        private final List<RowItem> rows = new ArrayList<>();
        private String selected = "";

        DeviceList() {
            super(MainActivity.this);
            setOrientation(LinearLayout.VERTICAL);
        }

        void render(List<PeerEntry> paired, List<PeerEntry> lan, String note) {
            removeAllViews();
            rows.clear();
            boolean any = false;
            if (paired != null) {
                for (PeerEntry e : paired) {
                    addRow(e.name, e.sub, e.value);
                    any = true;
                }
            }
            if (lan != null) {
                for (PeerEntry e : lan) {
                    addRow(e.name, e.sub, e.value);
                    any = true;
                }
            }
            if (note != null && !note.isEmpty()) {
                addNote(note);
                any = true;
            }
            if (!any) {
                addNote("暂无设备 · 去「设备」页完成一次配对，或点右上角搜索局域网");
            }
            syncSelection();
        }

        void syncSelection() {
            selected = currentTarget();
            paint();
        }

        private void addRow(final String name, final String sub, final String value) {
            LinearLayout row = new LinearLayout(MainActivity.this);
            row.setOrientation(LinearLayout.HORIZONTAL);
            row.setGravity(Gravity.CENTER_VERTICAL);
            row.setPadding(dp(12), dp(10), dp(12), dp(10));

            String initial = name.isEmpty() ? "?" : name.substring(0, 1).toUpperCase(Locale.US);
            TextView avatar = label(initial, 15, cOnPrimaryContainer, true);
            avatar.setGravity(Gravity.CENTER);
            avatar.setBackground(oval(cPrimaryContainer));
            row.addView(avatar, new LinearLayout.LayoutParams(dp(40), dp(40)));

            LinearLayout col = new LinearLayout(MainActivity.this);
            col.setOrientation(LinearLayout.VERTICAL);
            TextView nm = label(name, 15, cOnSurface, true);
            nm.setSingleLine(true);
            nm.setEllipsize(TextUtils.TruncateAt.END);
            col.addView(nm, new LinearLayout.LayoutParams(MATCH, WRAP));

            TextView sb = label(sub, 11, cOnSurfaceVariant, false);
            sb.setTypeface(Typeface.MONOSPACE);
            sb.setSingleLine(true);
            sb.setEllipsize(TextUtils.TruncateAt.END);
            LinearLayout.LayoutParams sbLp = new LinearLayout.LayoutParams(MATCH, WRAP);
            sbLp.topMargin = dp(2);
            col.addView(sb, sbLp);

            LinearLayout.LayoutParams colLp = new LinearLayout.LayoutParams(0, WRAP, 1f);
            colLp.leftMargin = dp(12);
            row.addView(col, colLp);

            TextView mark = label("已选", 11, cOnPrimary, true);
            mark.setPadding(dp(8), dp(4), dp(8), dp(4));
            mark.setBackground(roundBox(cPrimary, 0, 99));
            mark.setVisibility(View.GONE);
            LinearLayout.LayoutParams mkLp = new LinearLayout.LayoutParams(WRAP, WRAP);
            mkLp.leftMargin = dp(8);
            row.addView(mark, mkLp);

            row.setOnClickListener(v -> {
                selected = value;
                if (peerField != null) {
                    peerField.setText(value);
                }
                paint();
                updateTargetSummary();
            });

            LinearLayout.LayoutParams rowLp = new LinearLayout.LayoutParams(MATCH, WRAP);
            if (!rows.isEmpty()) {
                rowLp.topMargin = dp(6);
            }
            addView(row, rowLp);
            rows.add(new RowItem(value, row, mark));
        }

        private void addNote(String note) {
            TextView t = label(note, 12, cOnSurfaceVariant, false);
            LinearLayout.LayoutParams p = new LinearLayout.LayoutParams(MATCH, WRAP);
            p.topMargin = dp(6);
            addView(t, p);
        }

        private void paint() {
            for (RowItem r : rows) {
                boolean on = r.value.equals(selected) && !selected.isEmpty();
                int bg = on ? cSecondaryContainer : cSurface;
                r.row.setBackground(ripple(roundBox(bg, 0, 16), 16));
                r.mark.setVisibility(on ? View.VISIBLE : View.GONE);
            }
        }
    }

    private static class RowItem {
        final String value;
        final LinearLayout row;
        final TextView mark;

        RowItem(String value, LinearLayout row, TextView mark) {
            this.value = value;
            this.row = row;
            this.mark = mark;
        }
    }

    // ================= MD3 Component Builders =================

    private LinearLayout md3Card(int bg, float radiusDp, int l, int t, int r, int b) {
        LinearLayout c = new LinearLayout(this);
        c.setOrientation(LinearLayout.VERTICAL);
        c.setPadding(dp(l), dp(t), dp(r), dp(b));
        c.setBackground(roundBox(bg, 0, radiusDp));
        return c;
    }

    private TextView label(String text, float sp, int color, boolean medium) {
        TextView t = new TextView(this);
        t.setText(text);
        t.setTextSize(sp);
        t.setTextColor(color);
        t.setIncludeFontPadding(false);
        t.setTypeface(medium ? Typeface.create("sans-serif-medium", Typeface.NORMAL) : Typeface.DEFAULT);
        return t;
    }

    private EditText md3Input(String hint) {
        EditText e = new EditText(this);
        e.setHint(hint);
        e.setHintTextColor(cOnSurfaceVariant);
        e.setTextColor(cOnSurface);
        e.setTextSize(14);
        e.setSingleLine(true);
        e.setGravity(Gravity.CENTER_VERTICAL);
        e.setPadding(dp(14), 0, dp(14), 0);
        e.setBackground(roundBox(cSurface, cOutlineVariant, 14));
        return e;
    }

    private Button basePillBtn(String text, float sp) {
        Button b = new Button(this);
        b.setText(text);
        b.setTextSize(sp);
        b.setAllCaps(false);
        b.setIncludeFontPadding(false);
        b.setTypeface(Typeface.create("sans-serif-medium", Typeface.NORMAL));
        b.setGravity(Gravity.CENTER);
        b.setPadding(dp(16), 0, dp(16), 0);
        b.setMinHeight(0);
        b.setMinimumHeight(0);
        b.setMinWidth(0);
        b.setMinimumWidth(0);
        b.setElevation(0);
        b.setStateListAnimator(null);
        return b;
    }

    private Button pillFilledBtn(String text, int bg, int fg) {
        Button b = basePillBtn(text, 14);
        applyFilledStyle(b, bg, fg);
        return b;
    }

    private Button pillSmallBtn(String text, int bg, int fg) {
        Button b = basePillBtn(text, 12);
        b.setPadding(dp(12), 0, dp(12), 0);
        applyFilledStyle(b, bg, fg);
        return b;
    }

    private void applyFilledStyle(Button b, int bg, int fg) {
        b.setBackground(ripple(roundBox(bg, 0, 99), 99));
        b.setTextColor(new ColorStateList(
                new int[][]{{-android.R.attr.state_enabled}, {}},
                new int[]{cOutline, fg}));
    }

    private Button pillOutlineBtn(String text) {
        Button b = basePillBtn(text, 13);
        b.setBackground(ripple(roundBox(cSurface, cOutline, 99), 99));
        b.setTextColor(cPrimary);
        return b;
    }

    private GradientDrawable roundBox(int fill, int stroke, float radiusDp) {
        GradientDrawable d = new GradientDrawable();
        d.setShape(GradientDrawable.RECTANGLE);
        d.setColor(fill);
        d.setCornerRadius(dp(radiusDp));
        if (stroke != 0) {
            d.setStroke(dp(1), stroke);
        }
        return d;
    }

    private GradientDrawable oval(int color) {
        GradientDrawable d = new GradientDrawable();
        d.setShape(GradientDrawable.OVAL);
        d.setColor(color);
        return d;
    }

    private Drawable ripple(Drawable content, float radiusDp) {
        return new RippleDrawable(ColorStateList.valueOf(cRipple), content,
                roundBox(Color.WHITE, 0, radiusDp));
    }

    private TextView pathBadgeView(String via) {
        TextView t = label(pathLabel(via), 11, cOnPrimaryContainer, true);
        t.setPadding(dp(8), dp(3), dp(8), dp(3));
        stylePathBadge(t, via);
        return t;
    }

    private void stylePathBadge(TextView t, String via) {
        String label = pathLabel(via);
        t.setText(label);
        if ("lan".equalsIgnoreCase(via)) {
            t.setTextColor(cOnPrimaryContainer);
            t.setBackground(roundBox(cPrimaryContainer, 0, 99));
        } else if ("punch".equalsIgnoreCase(via)) {
            t.setTextColor(cWarn);
            t.setBackground(roundBox(cWarnContainer, 0, 99));
        } else {
            t.setTextColor(cOnTertiaryContainer);
            t.setBackground(roundBox(cTertiaryContainer, 0, 99));
        }
    }

    private static String pathLabel(String path) {
        if ("lan".equalsIgnoreCase(path)) {
            return "LAN";
        }
        if ("punch".equalsIgnoreCase(path)) {
            return "打洞";
        }
        if ("relay".equalsIgnoreCase(path)) {
            return "中继";
        }
        return path == null ? "" : path;
    }

    /** Geometric nav icons drawn via Android Canvas so rendering is crisp at all DPIs. */
    private class NavIcon extends Drawable {
        private final int kind;
        private final Paint paint = new Paint(Paint.ANTI_ALIAS_FLAG);

        NavIcon(int kind, int color) {
            this.kind = kind;
            paint.setColor(color);
            paint.setStyle(Paint.Style.STROKE);
            paint.setStrokeWidth(dp(1.9f));
            paint.setStrokeCap(Paint.Cap.ROUND);
            paint.setStrokeJoin(Paint.Join.ROUND);
        }

        @Override
        public void draw(Canvas canvas) {
            android.graphics.Rect b = getBounds();
            float w = b.width();
            float h = b.height();
            Path p = new Path();
            if (kind == 0) { // Send: diagonal arrow up-right inside tray/open
                p.moveTo(w * 0.22f, h * 0.78f);
                p.lineTo(w * 0.78f, h * 0.22f);
                p.moveTo(w * 0.36f, h * 0.22f);
                p.lineTo(w * 0.78f, h * 0.22f);
                p.lineTo(w * 0.78f, h * 0.64f);
            } else if (kind == 1) { // Receive: downward arrow into tray
                p.moveTo(w * 0.50f, h * 0.18f);
                p.lineTo(w * 0.50f, h * 0.65f);
                p.moveTo(w * 0.30f, h * 0.47f);
                p.lineTo(w * 0.50f, h * 0.67f);
                p.lineTo(w * 0.70f, h * 0.47f);
                p.moveTo(w * 0.20f, h * 0.74f);
                p.lineTo(w * 0.20f, h * 0.84f);
                p.lineTo(w * 0.80f, h * 0.84f);
                p.lineTo(w * 0.80f, h * 0.74f);
            } else { // Devices / Pair: linked rounded squares
                canvas.drawRoundRect(w * 0.16f, h * 0.26f, w * 0.58f, h * 0.74f, w * 0.10f, h * 0.10f, paint);
                canvas.drawRoundRect(w * 0.42f, h * 0.26f, w * 0.84f, h * 0.74f, w * 0.10f, h * 0.10f, paint);
                return;
            }
            canvas.drawPath(p, paint);
        }

        @Override public void setAlpha(int alpha) {}
        @Override public void setColorFilter(ColorFilter cf) {}
        @Override public int getOpacity() { return PixelFormat.TRANSLUCENT; }
    }

    // ================= Platform & Utility Helpers =================

    private void startDotPulse() {
        if (dotAnimator != null) {
            dotAnimator.cancel();
        }
        dotAnimator = ValueAnimator.ofFloat(1f, 0.45f);
        dotAnimator.setDuration(800);
        dotAnimator.setRepeatMode(ValueAnimator.REVERSE);
        dotAnimator.setRepeatCount(ValueAnimator.INFINITE);
        dotAnimator.setInterpolator(new AccelerateDecelerateInterpolator());
        dotAnimator.addUpdateListener(a -> {
            if (topDot != null) {
                topDot.setAlpha((Float) a.getAnimatedValue());
            }
        });
        dotAnimator.start();
    }

    private void stopDotPulse() {
        if (dotAnimator != null) {
            dotAnimator.cancel();
            dotAnimator = null;
        }
        if (topDot != null) {
            topDot.setAlpha(1f);
        }
    }

    private void startKeepAlive(String text) {
        Intent i = new Intent(this, TransferService.class).putExtra("text", text);
        if (Build.VERSION.SDK_INT >= 26) {
            startForegroundService(i);
        } else {
            startService(i);
        }
    }

    private void stopKeepAlive() {
        stopService(new Intent(this, TransferService.class));
    }

    private void acquireMulticastLock() {
        try {
            WifiManager wm = (WifiManager) getApplicationContext().getSystemService(WIFI_SERVICE);
            if (wm != null) {
                multicastLock = wm.createMulticastLock("ft-lan");
                multicastLock.setReferenceCounted(false);
                multicastLock.acquire();
            }
        } catch (Exception e) {
            Log.w(TAG, "multicast lock", e);
        }
    }

    private void releaseMulticastLock() {
        try {
            if (multicastLock != null && multicastLock.isHeld()) {
                multicastLock.release();
            }
        } catch (Exception e) {
            Log.w(TAG, "multicast release", e);
        }
    }

    private void requestBatteryExemption() {
        try {
            android.os.PowerManager pm = (android.os.PowerManager) getSystemService(POWER_SERVICE);
            if (pm != null && pm.isIgnoringBatteryOptimizations(getPackageName())) {
                toast("系统已允许后台保活运行");
                return;
            }
            Intent i = new Intent(Settings.ACTION_REQUEST_IGNORE_BATTERY_OPTIMIZATIONS);
            i.setData(Uri.parse("package:" + getPackageName()));
            startActivity(i);
        } catch (Exception e) {
            try {
                startActivity(new Intent(Settings.ACTION_IGNORE_BATTERY_OPTIMIZATION_SETTINGS));
            } catch (Exception e2) {
                toast("请在系统设置中允许轻渡后台运行");
            }
        }
    }

    private String lanHint() {
        try {
            List<NetworkInterface> nifs = Collections.list(NetworkInterface.getNetworkInterfaces());
            for (int pass = 0; pass < 2; pass++) {
                for (NetworkInterface ni : nifs) {
                    String n = ni.getName();
                    if (n == null || !ni.isUp() || ni.isLoopback() || ni.isVirtual()) {
                        continue;
                    }
                    if (n.startsWith("rmnet") || n.startsWith("p2p") || n.startsWith("dummy")
                            || n.startsWith("tun") || n.startsWith("ip6tnl")) {
                        continue;
                    }
                    if (pass == 0 && !n.toLowerCase(Locale.US).startsWith("wlan")) {
                        continue;
                    }
                    for (InterfaceAddress ia : ni.getInterfaceAddresses()) {
                        InetAddress a = ia.getAddress();
                        if (a instanceof Inet4Address && !a.isLoopbackAddress() && !a.isLinkLocalAddress()) {
                            JSONObject o = new JSONObject();
                            o.put("name", n);
                            o.put("index", ni.getIndex());
                            o.put("ip", a.getHostAddress());
                            o.put("prefix", ia.getNetworkPrefixLength());
                            return o.toString();
                        }
                    }
                }
            }
        } catch (Exception e) {
            Log.w(TAG, "lanHint", e);
        }
        return "";
    }

    private static boolean isAutoTarget(String peer) {
        return HEX64.matcher(peer).matches();
    }

    private static boolean isLanTarget(String peer) {
        int i = peer.lastIndexOf(':');
        if (i <= 0 || i >= peer.length() - 1) {
            return false;
        }
        String port = peer.substring(i + 1);
        for (int k = 0; k < port.length(); k++) {
            char c = port.charAt(k);
            if (c < '0' || c > '9') {
                return false;
            }
        }
        return true;
    }

    private static String shortPeerId(String uri) {
        try {
            String rest = uri.substring(uri.indexOf("://") + 3);
            String[] parts = rest.split("/");
            if (parts.length >= 3 && parts[1].length() >= 12) {
                return parts[1].substring(0, 12) + "…";
            }
        } catch (RuntimeException ignored) {
        }
        return uri;
    }

    private File copyToCache(Uri uri, File dir) throws Exception {
        String name = displayName(uri);
        if (!dir.exists() && !dir.mkdirs()) {
            throw new Exception("无法创建缓存目录");
        }
        File f = new File(dir, name);
        try (InputStream in = getContentResolver().openInputStream(uri);
             OutputStream out = new FileOutputStream(f)) {
            if (in == null) {
                throw new Exception("无法读取 " + uri);
            }
            copyStream(in, out);
        }
        return f;
    }

    private String displayName(Uri uri) {
        String name = null;
        try (Cursor c = getContentResolver().query(uri, null, null, null, null)) {
            if (c != null && c.moveToFirst()) {
                int idx = c.getColumnIndex(OpenableColumns.DISPLAY_NAME);
                if (idx >= 0) {
                    name = c.getString(idx);
                }
            }
        } catch (Exception e) {
            Log.w(TAG, "query name", e);
        }
        if (name == null || name.isEmpty()) {
            name = uri.getLastPathSegment();
        }
        if (name == null || name.isEmpty()) {
            name = "file-" + System.currentTimeMillis();
        }
        return name.replace('/', '_').replace('\\', '_');
    }

    private static void deleteTree(File f, File dir) {
        if (f != null && f.exists()) {
            f.delete();
        }
        if (dir != null && dir.exists()) {
            dir.delete();
        }
    }

    private static String fmtBytes(long n) {
        if (n < 1024) {
            return n + " B";
        }
        if (n < 1048576) {
            return String.format(Locale.US, "%.1f KB", n / 1024.0);
        }
        if (n < 1073741824) {
            return String.format(Locale.US, "%.1f MB", n / 1048576.0);
        }
        return String.format(Locale.US, "%.2f GB", n / 1073741824.0);
    }

    private void copyText(String label, String value, String okMsg) {
        if (value == null || value.isEmpty()) {
            return;
        }
        ClipboardManager cm = (ClipboardManager) getSystemService(CLIPBOARD_SERVICE);
        if (cm != null) {
            cm.setPrimaryClip(ClipData.newPlainText(label, value));
            toast(okMsg);
        }
    }

    private void toast(String msg) {
        Toast.makeText(this, msg, Toast.LENGTH_SHORT).show();
    }

    private int dp(float v) {
        return Math.round(v * getResources().getDisplayMetrics().density);
    }
}
