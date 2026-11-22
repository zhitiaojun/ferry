package cn.cwlindean.ft;

import android.Manifest;
import android.animation.ValueAnimator;
import android.app.Activity;
import android.app.AlertDialog;
import android.app.Dialog;
import android.content.ClipData;
import android.content.ClipboardManager;
import android.content.ContentValues;
import android.content.Intent;
import android.content.SharedPreferences;
import android.content.pm.PackageManager;
import android.content.res.ColorStateList;
import android.content.res.Configuration;
import android.database.Cursor;
import android.graphics.Bitmap;
import android.graphics.BitmapFactory;
import android.graphics.Canvas;
import android.graphics.Color;
import android.graphics.ColorFilter;
import android.graphics.ImageFormat;
import android.graphics.Matrix;
import android.graphics.Outline;
import android.graphics.Paint;
import android.graphics.Path;
import android.graphics.PixelFormat;
import android.graphics.RectF;
import android.graphics.SurfaceTexture;
import android.graphics.Typeface;
import android.graphics.drawable.ColorDrawable;
import android.graphics.drawable.Drawable;
import android.graphics.drawable.GradientDrawable;
import android.graphics.drawable.RippleDrawable;
import android.hardware.camera2.CameraCaptureSession;
import android.hardware.camera2.CameraCharacteristics;
import android.hardware.camera2.CameraDevice;
import android.hardware.camera2.CameraManager;
import android.hardware.camera2.CaptureRequest;
import android.hardware.camera2.params.StreamConfigurationMap;
import android.media.Image;
import android.media.ImageReader;
import android.net.Uri;
import android.net.wifi.WifiManager;
import android.os.Build;
import android.os.Bundle;
import android.os.CountDownTimer;
import android.os.Environment;
import android.os.Handler;
import android.os.HandlerThread;
import android.os.Looper;
import android.os.SystemClock;
import android.provider.MediaStore;
import android.provider.OpenableColumns;
import android.provider.Settings;
import android.text.InputFilter;
import android.text.InputType;
import android.text.TextUtils;
import android.util.Log;
import android.util.Size;
import android.view.Gravity;
import android.view.Surface;
import android.view.TextureView;
import android.view.View;
import android.view.ViewGroup;
import android.view.ViewOutlineProvider;
import android.view.Window;
import android.view.WindowManager;
import android.view.animation.AccelerateDecelerateInterpolator;
import android.webkit.MimeTypeMap;
import android.widget.Button;
import android.widget.EditText;
import android.widget.FrameLayout;
import android.widget.HorizontalScrollView;
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
import java.nio.ByteBuffer;
import java.text.SimpleDateFormat;
import java.util.ArrayList;
import java.util.Arrays;
import java.util.Collections;
import java.util.Date;
import java.util.List;
import java.util.Locale;
import java.util.concurrent.ExecutorService;
import java.util.concurrent.Executors;
import java.util.concurrent.atomic.AtomicBoolean;
import java.util.regex.Pattern;

import mobile.Mobile;

/**
 * 轻渡 Ferry Android UI shell.
 * Transport, discovery, relay signaling, QR encode/decode and crypto identity live in the Go AAR (core/mobile).
 */
public class MainActivity extends Activity {
    private static final String TAG = "ft";
    private static final String SIGNAL_URL = "wss://ft.cwlindean.cn/signal";
    private static final int PICK_FILES = 42;
    private static final int PICK_PAIR_FILE = 43;
    private static final int REQ_CAMERA = 44;
    private static final int PAIR_TTL_SEC = 600;
    private static final int MATCH = ViewGroup.LayoutParams.MATCH_PARENT;
    private static final int WRAP = ViewGroup.LayoutParams.WRAP_CONTENT;
    private static final Pattern HEX64 = Pattern.compile("^[0-9a-fA-F]{64}$");

    // Theme tokens (v2 light/dark aesthetic matching ferry_mobile_redesign.html)
    private int cPrimary, cPrimaryPressed, cOnPrimary, cPrimaryContainer, cOnPrimaryContainer;
    private int cSurface, cCard, cSurfaceAlt, cSheetBg, cBorder;
    private int cOnSurface, cOnSurfaceVariant, cOutline;
    private int cError, cOnError, cErrorContainer, cOnErrorContainer;
    private int cWarn, cWarnContainer, cRipple;

    // App state
    private final Handler ui = new Handler(Looper.getMainLooper());
    private final ExecutorService ioPool = Executors.newSingleThreadExecutor();
    private WifiManager.MulticastLock multicastLock;
    private String deviceName = "";
    private String fullDeviceId = "";
    private String webShareUrl = "";
    private boolean recvRunning;
    private boolean webRunning;
    private boolean lanSearching;
    private int activePage; // 0 = Home, 1 = Settings
    private String lanPort = "";
    private String selectedTarget = "";
    private String activeFilter = "all"; // "all" | "recv" | "send"
    private List<Uri> sharedUris;
    private final List<PeerEntry> pairedEntries = new ArrayList<>();
    private final List<PeerEntry> lanEntries = new ArrayList<>();
    private final List<ReceivedEntry> recentEntries = new ArrayList<>();

    // Top Bar views
    private View backBtn;
    private View brandMarkView;
    private TextView topTitleView;
    private View topActionsWrap;

    // Pages
    private ScrollView pageHome;
    private ScrollView pageSettings;

    // Home views
    private HorizontalScrollView peerScroll;
    private LinearLayout peerChipRow;
    private LinearLayout sendCard;
    private LinearLayout unpairedCard;
    private TextView sendTargetTitle;
    private TextView sendRecvStatusBtn;
    private View recvPulseDot;
    private ValueAnimator dotAnimator;
    private TextView shareBannerView;
    private LinearLayout sendPickTile;
    private LinearLayout sendClipTile;
    private LinearLayout sendResultCard;
    private TextView sendResultBadge;
    private TextView sendResultText;

    // Recent list views
    private final TextView[] filterTabs = new TextView[3];
    private LinearLayout recentListView;
    private LinearLayout recentEmptyView;

    // Settings views
    private Button setRecvBtn;
    private Button setWebBtn;
    private TextView setWebUrlView;
    private TextView setNameVal;
    private TextView setIdVal;
    private TextView setPortVal;

    // Pairing Dialog state & views
    private Dialog pairDialog;
    private int pairMode; // 0 = scan, 1 = show QR, 2 = enter code
    private View modeScanWrap, modeShowWrap, modeEnterWrap;
    private TextureView scanTextureView;
    private TextView scanStatusLabel;
    private LinearLayout scanPermOverlay;
    private ImageView showQrImage;
    private LinearLayout showExpiredMask;
    private TextView showCodeText;
    private TextView showTtlText;
    private EditText enterCodeField;
    private EditText enterPeerField;
    private TextView enterStatusView;
    private Button enterSubmitBtn;
    private CountDownTimer pairCountdown;
    private int pairShowSeq;

    // Camera2 QR scanner state
    private volatile boolean cameraWanted;
    private HandlerThread camThread;
    private Handler camHandler;
    private CameraDevice cameraDevice;
    private CameraCaptureSession captureSession;
    private ImageReader imageReader;
    private Size previewSize = new Size(960, 720);
    private final AtomicBoolean decodingFrame = new AtomicBoolean(false);
    private final AtomicBoolean scanSucceeded = new AtomicBoolean(false);
    private long lastScanTickMs;
    private String lastRejectedQR = "";
    private long lastRejectMs;

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
        loadThemeTokens();
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

        if (getPreferences(MODE_PRIVATE).getBoolean("recvAuto", true)) {
            startAllReceive();
        }
    }

    @Override
    protected void onResume() {
        super.onResume();
        pollReceivedOnce();
        if (pairDialog != null && pairDialog.isShowing() && pairMode == 0) {
            startCameraIfReady();
        }
    }

    @Override
    protected void onPause() {
        stopCamera();
        super.onPause();
    }

    @Override
    protected void onNewIntent(Intent intent) {
        super.onNewIntent(intent);
        setIntent(intent);
        handleShareIntent(intent);
    }

    @Override
    public void onBackPressed() {
        if (pairDialog != null && pairDialog.isShowing()) {
            closePairSheet();
            return;
        }
        if (activePage == 1) {
            showPage(0);
            return;
        }
        super.onBackPressed();
    }

    @Override
    protected void onDestroy() {
        closePairSheet();
        ui.removeCallbacks(pollTask);
        stopDotPulse();
        releaseMulticastLock();
        ioPool.shutdownNow();
        super.onDestroy();
    }

    // ================= App Directories (Key Isolation) =================

    private File stateDir() {
        File d = new File(getFilesDir(), "state");
        if (!d.exists()) {
            d.mkdirs();
        }
        return d;
    }

    private File inboxDir() {
        File d = new File(getFilesDir(), "inbox");
        if (!d.exists()) {
            d.mkdirs();
        }
        return d;
    }

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

    // ================= Theme Tokens & Chrome =================

    private boolean isNight() {
        return (getResources().getConfiguration().uiMode & Configuration.UI_MODE_NIGHT_MASK)
                == Configuration.UI_MODE_NIGHT_YES;
    }

    private void loadThemeTokens() {
        if (isNight()) {
            cPrimary = Color.parseColor("#14B8A6");
            cPrimaryPressed = Color.parseColor("#0D9488");
            cOnPrimary = Color.WHITE;
            cPrimaryContainer = Color.parseColor("#134E4A");
            cOnPrimaryContainer = Color.parseColor("#99F6E4");
            cSurface = Color.parseColor("#111827");
            cCard = Color.parseColor("#182230");
            cSurfaceAlt = Color.parseColor("#1F2937");
            cSheetBg = Color.parseColor("#EB182230");
            cBorder = Color.parseColor("#273244");
            cOnSurface = Color.parseColor("#F3F4F6");
            cOnSurfaceVariant = Color.parseColor("#9CA3AF");
            cOutline = Color.parseColor("#4B5563");
            cError = Color.parseColor("#F87171");
            cOnError = Color.parseColor("#450A0A");
            cErrorContainer = Color.parseColor("#7F1D1D");
            cOnErrorContainer = Color.parseColor("#FEE2E2");
            cWarn = Color.parseColor("#FBBF24");
            cWarnContainer = Color.parseColor("#451A03");
            cRipple = Color.parseColor("#26FFFFFF");
        } else {
            cPrimary = Color.parseColor("#0D9488");
            cPrimaryPressed = Color.parseColor("#0F766E");
            cOnPrimary = Color.WHITE;
            cPrimaryContainer = Color.parseColor("#CCFBF1");
            cOnPrimaryContainer = Color.parseColor("#134E4A");
            cSurface = Color.parseColor("#FFFFFF");
            cCard = Color.parseColor("#FAFAFA");
            cSurfaceAlt = Color.parseColor("#F3F4F6");
            cSheetBg = Color.parseColor("#F0FFFFFF");
            cBorder = Color.parseColor("#E5E7EB");
            cOnSurface = Color.parseColor("#111827");
            cOnSurfaceVariant = Color.parseColor("#6B7280");
            cOutline = Color.parseColor("#9CA3AF");
            cError = Color.parseColor("#DC2626");
            cOnError = Color.WHITE;
            cErrorContainer = Color.parseColor("#FEE2E2");
            cOnErrorContainer = Color.parseColor("#7F1D1D");
            cWarn = Color.parseColor("#B45309");
            cWarnContainer = Color.parseColor("#FEF3C7");
            cRipple = Color.parseColor("#18000000");
        }
    }

    private void applyWindowChrome() {
        try {
            getWindow().setStatusBarColor(cSurface);
            getWindow().setNavigationBarColor(cSurface);
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

    // ================= Build Redesigned UI (No Bottom Tabs) =================

    private View buildUi() {
        LinearLayout scaffold = new LinearLayout(this);
        scaffold.setOrientation(LinearLayout.VERTICAL);
        scaffold.setBackgroundColor(cSurface);
        scaffold.setFitsSystemWindows(true);

        scaffold.addView(buildTopBar(), new LinearLayout.LayoutParams(MATCH, dp(56)));

        FrameLayout contentHost = new FrameLayout(this);
        pageHome = buildHomePage();
        pageSettings = buildSettingsPage();
        pageSettings.setVisibility(View.GONE);
        contentHost.addView(pageHome, new FrameLayout.LayoutParams(MATCH, MATCH));
        contentHost.addView(pageSettings, new FrameLayout.LayoutParams(MATCH, MATCH));

        scaffold.addView(contentHost, new LinearLayout.LayoutParams(MATCH, 0, 1f));
        showPage(0);
        return scaffold;
    }

    private LinearLayout buildTopBar() {
        LinearLayout bar = new LinearLayout(this);
        bar.setOrientation(LinearLayout.HORIZONTAL);
        bar.setGravity(Gravity.CENTER_VERTICAL);
        bar.setPadding(dp(20), dp(4), dp(14), dp(4));

        // Left: back button (Settings) + brand mark (Home) + title
        ImageView backImg = new ImageView(this);
        backImg.setImageDrawable(new IconDrawable(IconDrawable.BACK, cOnSurface));
        backImg.setPadding(dp(6), dp(6), dp(6), dp(6));
        backImg.setBackground(ripple(oval(Color.TRANSPARENT), 99));
        backImg.setContentDescription("返回");
        backImg.setOnClickListener(v -> showPage(0));
        backImg.setVisibility(View.GONE);
        backBtn = backImg;
        LinearLayout.LayoutParams bkLp = new LinearLayout.LayoutParams(dp(36), dp(36));
        bkLp.rightMargin = dp(4);
        bar.addView(backImg, bkLp);

        ImageView mark = new ImageView(this);
        mark.setImageDrawable(new FerryMarkDrawable());
        brandMarkView = mark;
        LinearLayout.LayoutParams mkLp = new LinearLayout.LayoutParams(dp(22), dp(22));
        mkLp.rightMargin = dp(9);
        bar.addView(mark, mkLp);

        topTitleView = label("轻渡", 18, cOnSurface, true);
        bar.addView(topTitleView, new LinearLayout.LayoutParams(0, WRAP, 1f));

        // Right: minimal scan + settings icon actions
        LinearLayout actions = new LinearLayout(this);
        actions.setOrientation(LinearLayout.HORIZONTAL);
        actions.setGravity(Gravity.CENTER_VERTICAL);
        topActionsWrap = actions;

        ImageView scanBtn = iconActionBtn(IconDrawable.SCAN, "扫一扫", v -> openPairSheet(0));
        LinearLayout.LayoutParams scLp = new LinearLayout.LayoutParams(dp(40), dp(40));
        scLp.rightMargin = dp(2);
        actions.addView(scanBtn, scLp);

        ImageView settingsBtn = iconActionBtn(IconDrawable.SETTINGS, "设置", v -> showPage(1));
        actions.addView(settingsBtn, new LinearLayout.LayoutParams(dp(40), dp(40)));

        bar.addView(actions, new LinearLayout.LayoutParams(WRAP, WRAP));
        return bar;
    }

    private ImageView iconActionBtn(int kind, String desc, View.OnClickListener listener) {
        ImageView iv = new ImageView(this);
        iv.setImageDrawable(new IconDrawable(kind, cOnSurface));
        iv.setPadding(dp(9), dp(9), dp(9), dp(9));
        iv.setBackground(ripple(oval(Color.TRANSPARENT), 99));
        iv.setContentDescription(desc);
        iv.setOnClickListener(listener);
        return iv;
    }

    private void showPage(int page) {
        activePage = page;
        boolean isSet = page == 1;
        pageHome.setVisibility(isSet ? View.GONE : View.VISIBLE);
        pageSettings.setVisibility(isSet ? View.VISIBLE : View.GONE);
        backBtn.setVisibility(isSet ? View.VISIBLE : View.GONE);
        brandMarkView.setVisibility(isSet ? View.GONE : View.VISIBLE);
        topActionsWrap.setVisibility(isSet ? View.GONE : View.VISIBLE);
        topTitleView.setText(isSet ? "设置" : "轻渡");
        if (isSet) {
            refreshSettingsInfo();
        }
    }

    // ================= Page 0: Home =================

    private ScrollView buildHomePage() {
        ScrollView sv = new ScrollView(this);
        sv.setFillViewport(true);

        LinearLayout root = new LinearLayout(this);
        root.setOrientation(LinearLayout.VERTICAL);
        root.setPadding(dp(20), dp(8), dp(20), dp(28));

        // 1. Horizontal Paired / LAN Device Selector Bar
        peerScroll = new HorizontalScrollView(this);
        peerScroll.setHorizontalScrollBarEnabled(false);
        peerChipRow = new LinearLayout(this);
        peerChipRow.setOrientation(LinearLayout.HORIZONTAL);
        peerChipRow.setGravity(Gravity.CENTER_VERTICAL);
        peerScroll.addView(peerChipRow, new FrameLayout.LayoutParams(WRAP, WRAP));
        LinearLayout.LayoutParams psLp = new LinearLayout.LayoutParams(MATCH, WRAP);
        psLp.bottomMargin = dp(16);
        root.addView(peerScroll, psLp);

        // 2. Paired Send Card
        sendCard = new LinearLayout(this);
        sendCard.setOrientation(LinearLayout.VERTICAL);
        sendCard.setPadding(dp(16), dp(14), dp(16), dp(16));
        sendCard.setBackground(roundBox(cCard, cBorder, 16));

        LinearLayout cardTop = new LinearLayout(this);
        cardTop.setOrientation(LinearLayout.HORIZONTAL);
        cardTop.setGravity(Gravity.CENTER_VERTICAL);

        sendTargetTitle = label("发往 未选择设备", 13, cOnSurface, true);
        sendTargetTitle.setSingleLine(true);
        sendTargetTitle.setEllipsize(TextUtils.TruncateAt.END);
        cardTop.addView(sendTargetTitle, new LinearLayout.LayoutParams(0, WRAP, 1f));

        LinearLayout recvStatusWrap = new LinearLayout(this);
        recvStatusWrap.setOrientation(LinearLayout.HORIZONTAL);
        recvStatusWrap.setGravity(Gravity.CENTER_VERTICAL);
        recvStatusWrap.setPadding(dp(8), dp(4), dp(8), dp(4));
        recvStatusWrap.setBackground(ripple(roundBox(Color.TRANSPARENT, 0, 99), 99));
        recvStatusWrap.setOnClickListener(v -> toggleReceive());

        recvPulseDot = new View(this);
        recvPulseDot.setBackground(oval(cOnSurfaceVariant));
        LinearLayout.LayoutParams dotLp = new LinearLayout.LayoutParams(dp(7), dp(7));
        dotLp.rightMargin = dp(5);
        recvStatusWrap.addView(recvPulseDot, dotLp);

        sendRecvStatusBtn = label("未开启接收", 12, cOnSurfaceVariant, false);
        recvStatusWrap.addView(sendRecvStatusBtn, new LinearLayout.LayoutParams(WRAP, WRAP));
        cardTop.addView(recvStatusWrap, new LinearLayout.LayoutParams(WRAP, WRAP));
        sendCard.addView(cardTop, new LinearLayout.LayoutParams(MATCH, WRAP));

        shareBannerView = label("", 12, cOnPrimaryContainer, true);
        shareBannerView.setPadding(dp(12), dp(8), dp(12), dp(8));
        shareBannerView.setBackground(roundBox(cPrimaryContainer, 0, 10));
        shareBannerView.setVisibility(View.GONE);
        LinearLayout.LayoutParams sbLp = new LinearLayout.LayoutParams(MATCH, WRAP);
        sbLp.topMargin = dp(10);
        sendCard.addView(shareBannerView, sbLp);

        LinearLayout tileGrid = new LinearLayout(this);
        tileGrid.setOrientation(LinearLayout.HORIZONTAL);
        LinearLayout.LayoutParams tgLp = new LinearLayout.LayoutParams(MATCH, WRAP);
        tgLp.topMargin = dp(12);

        sendPickTile = actionTile("发文件 / 照片", IconDrawable.UPLOAD, true, v -> pickFiles());
        LinearLayout.LayoutParams t1Lp = new LinearLayout.LayoutParams(0, WRAP, 1f);
        t1Lp.rightMargin = dp(10);
        tileGrid.addView(sendPickTile, t1Lp);

        sendClipTile = actionTile("发剪贴板", IconDrawable.CLIP, false, v -> sendClipboard());
        tileGrid.addView(sendClipTile, new LinearLayout.LayoutParams(0, WRAP, 1f));
        sendCard.addView(tileGrid, tgLp);

        // Result feedback banner inside Send Card (hidden when idle)
        sendResultCard = new LinearLayout(this);
        sendResultCard.setOrientation(LinearLayout.HORIZONTAL);
        sendResultCard.setGravity(Gravity.CENTER_VERTICAL);
        sendResultCard.setPadding(dp(12), dp(9), dp(12), dp(9));
        sendResultCard.setVisibility(View.GONE);

        sendResultBadge = label("", 11, cOnPrimaryContainer, true);
        sendResultBadge.setPadding(dp(8), dp(3), dp(8), dp(3));
        LinearLayout.LayoutParams rbLp = new LinearLayout.LayoutParams(WRAP, WRAP);
        rbLp.rightMargin = dp(8);
        sendResultCard.addView(sendResultBadge, rbLp);

        sendResultText = label("", 12, cOnSurface, false);
        sendResultCard.addView(sendResultText, new LinearLayout.LayoutParams(0, WRAP, 1f));

        LinearLayout.LayoutParams srLp = new LinearLayout.LayoutParams(MATCH, WRAP);
        srLp.topMargin = dp(10);
        sendCard.addView(sendResultCard, srLp);

        LinearLayout.LayoutParams scLp = new LinearLayout.LayoutParams(MATCH, WRAP);
        scLp.bottomMargin = dp(20);
        root.addView(sendCard, scLp);

        // 3. Unpaired Onboarding Card
        unpairedCard = new LinearLayout(this);
        unpairedCard.setOrientation(LinearLayout.VERTICAL);
        unpairedCard.setGravity(Gravity.CENTER_HORIZONTAL);
        unpairedCard.setPadding(dp(20), dp(22), dp(20), dp(20));
        unpairedCard.setBackground(roundBox(cCard, cBorder, 16));

        TextView upTitle = label("先配对一台电脑", 15, cOnSurface, true);
        upTitle.setGravity(Gravity.CENTER);
        unpairedCard.addView(upTitle, new LinearLayout.LayoutParams(WRAP, WRAP));

        TextView upSub = label("在电脑轻渡点击「添加设备」，手机扫一下即可互传文件。", 12, cOnSurfaceVariant, false);
        upSub.setGravity(Gravity.CENTER);
        LinearLayout.LayoutParams usLp = new LinearLayout.LayoutParams(MATCH, WRAP);
        usLp.topMargin = dp(6);
        unpairedCard.addView(upSub, usLp);

        Button scanCta = filledBtn("扫码配对电脑", cPrimaryPressed, cOnPrimary);
        scanCta.setOnClickListener(v -> openPairSheet(0));
        LinearLayout.LayoutParams ctaLp = new LinearLayout.LayoutParams(MATCH, dp(42));
        ctaLp.topMargin = dp(14);
        unpairedCard.addView(scanCta, ctaLp);

        LinearLayout upLinks = new LinearLayout(this);
        upLinks.setOrientation(LinearLayout.HORIZONTAL);
        upLinks.setGravity(Gravity.CENTER);
        LinearLayout.LayoutParams ulLp = new LinearLayout.LayoutParams(MATCH, WRAP);
        ulLp.topMargin = dp(12);

        TextView manualLink = label("手动输入 6 位配对码", 12, cPrimary, true);
        manualLink.setPadding(dp(8), dp(4), dp(8), dp(4));
        manualLink.setOnClickListener(v -> openPairSheet(2));
        upLinks.addView(manualLink, new LinearLayout.LayoutParams(WRAP, WRAP));

        TextView dotSep = label("·", 12, cOnSurfaceVariant, false);
        dotSep.setPadding(dp(4), 0, dp(4), 0);
        upLinks.addView(dotSep, new LinearLayout.LayoutParams(WRAP, WRAP));

        TextView lanLink = label("搜索同局域网设备", 12, cOnSurfaceVariant, false);
        lanLink.setPadding(dp(8), dp(4), dp(8), dp(4));
        lanLink.setOnClickListener(v -> discover());
        upLinks.addView(lanLink, new LinearLayout.LayoutParams(WRAP, WRAP));
        unpairedCard.addView(upLinks, ulLp);

        LinearLayout.LayoutParams ucLp = new LinearLayout.LayoutParams(MATCH, WRAP);
        ucLp.bottomMargin = dp(20);
        root.addView(unpairedCard, ucLp);

        // 4. Recent Transfers Section Header + Filters
        LinearLayout recHdr = new LinearLayout(this);
        recHdr.setOrientation(LinearLayout.HORIZONTAL);
        recHdr.setGravity(Gravity.CENTER_VERTICAL);
        recHdr.setPadding(0, 0, 0, dp(10));

        recHdr.addView(label("最近记录", 15, cOnSurface, true), new LinearLayout.LayoutParams(0, WRAP, 1f));

        String[] fKeys = {"all", "recv", "send"};
        String[] fNames = {"全部", "已收到", "已发出"};
        for (int i = 0; i < 3; i++) {
            final String fk = fKeys[i];
            TextView tv = label(fNames[i], 13, cOnSurfaceVariant, true);
            tv.setPadding(dp(10), dp(4), i == 2 ? 0 : dp(10), dp(4));
            tv.setOnClickListener(v -> {
                activeFilter = fk;
                renderFilterTabs();
                renderRecentList();
            });
            filterTabs[i] = tv;
            recHdr.addView(tv, new LinearLayout.LayoutParams(WRAP, WRAP));
        }
        root.addView(recHdr, new LinearLayout.LayoutParams(MATCH, WRAP));

        View divider = new View(this);
        divider.setBackgroundColor(cBorder);
        root.addView(divider, new LinearLayout.LayoutParams(MATCH, 1));

        recentListView = new LinearLayout(this);
        recentListView.setOrientation(LinearLayout.VERTICAL);
        root.addView(recentListView, new LinearLayout.LayoutParams(MATCH, WRAP));

        recentEmptyView = new LinearLayout(this);
        recentEmptyView.setOrientation(LinearLayout.VERTICAL);
        recentEmptyView.setGravity(Gravity.CENTER_HORIZONTAL);
        recentEmptyView.setPadding(dp(16), dp(36), dp(16), dp(24));
        TextView empTitle = label("还没有传输记录", 13, cOnSurface, true);
        recentEmptyView.addView(empTitle, new LinearLayout.LayoutParams(WRAP, WRAP));
        TextView empSub = label("收到的文件会自动放进系统 Download/Ferry 文件夹。", 12, cOnSurfaceVariant, false);
        empSub.setGravity(Gravity.CENTER);
        LinearLayout.LayoutParams esLp = new LinearLayout.LayoutParams(WRAP, WRAP);
        esLp.topMargin = dp(4);
        recentEmptyView.addView(empSub, esLp);
        root.addView(recentEmptyView, new LinearLayout.LayoutParams(MATCH, WRAP));

        renderPeerSection();
        renderFilterTabs();
        renderRecentList();
        refreshReceiveUi();

        sv.addView(root, new FrameLayout.LayoutParams(MATCH, WRAP));
        return sv;
    }

    private LinearLayout actionTile(String text, int iconKind, boolean primary, View.OnClickListener click) {
        LinearLayout tile = new LinearLayout(this);
        tile.setOrientation(LinearLayout.VERTICAL);
        tile.setGravity(Gravity.CENTER);
        tile.setPadding(dp(12), dp(16), dp(12), dp(16));
        Drawable bg = primary
                ? roundBox(cPrimaryPressed, 0, 12)
                : roundBox(cSurface, cBorder, 12);
        tile.setBackground(ripple(bg, 12));
        tile.setOnClickListener(click);

        ImageView iv = new ImageView(this);
        iv.setImageDrawable(new IconDrawable(iconKind, primary ? cOnPrimary : cOnSurface));
        LinearLayout.LayoutParams ivLp = new LinearLayout.LayoutParams(dp(22), dp(22));
        ivLp.bottomMargin = dp(7);
        tile.addView(iv, ivLp);

        TextView tv = label(text, 13, primary ? cOnPrimary : cOnSurface, true);
        tile.addView(tv, new LinearLayout.LayoutParams(WRAP, WRAP));
        return tile;
    }

    // ================= Page 1: Settings (Local Device Info lives here) =================

    private ScrollView buildSettingsPage() {
        ScrollView sv = new ScrollView(this);
        sv.setFillViewport(true);

        LinearLayout root = new LinearLayout(this);
        root.setOrientation(LinearLayout.VERTICAL);
        root.setPadding(dp(20), dp(10), dp(20), dp(32));

        // Group 1: Receive + WebShare + Battery
        LinearLayout grp1 = new LinearLayout(this);
        grp1.setOrientation(LinearLayout.VERTICAL);
        grp1.setBackground(roundBox(cCard, cBorder, 16));

        setRecvBtn = smallOutlineBtn("开启中", v -> toggleReceive());
        grp1.addView(settingActionRow("后台接收文件", "自动存入系统 Download/Ferry", setRecvBtn, null),
                new LinearLayout.LayoutParams(MATCH, WRAP));

        grp1.addView(hairline(), new LinearLayout.LayoutParams(MATCH, 1));

        setWebBtn = smallOutlineBtn("开启", v -> toggleWebShare());
        grp1.addView(settingActionRow("网页快传", "临时给未安装软件的设备传文件", setWebBtn, v -> toggleWebShare()),
                new LinearLayout.LayoutParams(MATCH, WRAP));

        setWebUrlView = label("", 12, cPrimary, true);
        setWebUrlView.setPadding(dp(16), dp(4), dp(16), dp(14));
        setWebUrlView.setVisibility(View.GONE);
        setWebUrlView.setOnClickListener(v -> copyText("网页分享地址", webShareUrl, "已复制局域网网址，请在浏览器打开"));
        grp1.addView(setWebUrlView, new LinearLayout.LayoutParams(MATCH, WRAP));

        grp1.addView(hairline(), new LinearLayout.LayoutParams(MATCH, 1));

        Button batBtn = smallOutlineBtn("去设置", v -> requestBatteryExemption());
        grp1.addView(settingActionRow("电池后台白名单", "减少系统休眠导致的接收断开", batBtn, v -> requestBatteryExemption()),
                new LinearLayout.LayoutParams(MATCH, WRAP));

        LinearLayout.LayoutParams g1Lp = new LinearLayout.LayoutParams(MATCH, WRAP);
        g1Lp.bottomMargin = dp(22);
        root.addView(grp1, g1Lp);

        // Group 2: 本机信息 (Technical info tucked out of daily Home)
        TextView infoHdr = label("本机信息", 12, cOnSurfaceVariant, true);
        infoHdr.setPadding(dp(4), 0, dp(4), dp(8));
        root.addView(infoHdr, new LinearLayout.LayoutParams(MATCH, WRAP));

        LinearLayout grp2 = new LinearLayout(this);
        grp2.setOrientation(LinearLayout.VERTICAL);
        grp2.setBackground(roundBox(cCard, cBorder, 16));

        setNameVal = label(deviceName, 13, cOnSurface, true);
        grp2.addView(settingInfoRow("设备名称（点击修改）", setNameVal, v -> showRenameDialog()),
                new LinearLayout.LayoutParams(MATCH, WRAP));
        grp2.addView(hairline(), new LinearLayout.LayoutParams(MATCH, 1));

        setIdVal = label("读取中…", 13, cOnSurface, false);
        setIdVal.setTypeface(Typeface.MONOSPACE);
        grp2.addView(settingInfoRow("设备 ID", setIdVal, v ->
                        copyText("设备 ID", fullDeviceId, "已复制完整设备 ID")),
                new LinearLayout.LayoutParams(MATCH, WRAP));
        grp2.addView(hairline(), new LinearLayout.LayoutParams(MATCH, 1));

        setPortVal = label("未开启", 13, cOnSurface, false);
        setPortVal.setTypeface(Typeface.MONOSPACE);
        grp2.addView(settingInfoRow("本地监听端口", setPortVal, null),
                new LinearLayout.LayoutParams(MATCH, WRAP));
        grp2.addView(hairline(), new LinearLayout.LayoutParams(MATCH, 1));

        TextView verVal = label(Mobile.Version, 13, cOnSurfaceVariant, false);
        verVal.setTypeface(Typeface.MONOSPACE);
        grp2.addView(settingInfoRow("版本", verVal, null),
                new LinearLayout.LayoutParams(MATCH, WRAP));

        LinearLayout.LayoutParams g2Lp = new LinearLayout.LayoutParams(MATCH, WRAP);
        g2Lp.bottomMargin = dp(18);
        root.addView(grp2, g2Lp);

        // Legacy pair file import fallback
        TextView importLegacyBtn = label("导入旧版配对文件 (.txt)", 12, cOnSurfaceVariant, false);
        importLegacyBtn.setGravity(Gravity.CENTER);
        importLegacyBtn.setPadding(dp(12), dp(10), dp(12), dp(10));
        importLegacyBtn.setBackground(ripple(roundBox(Color.TRANSPARENT, 0, 12), 12));
        importLegacyBtn.setOnClickListener(v -> pickPairFile());
        root.addView(importLegacyBtn, new LinearLayout.LayoutParams(MATCH, WRAP));

        sv.addView(root, new FrameLayout.LayoutParams(MATCH, WRAP));
        return sv;
    }

    private LinearLayout settingActionRow(String title, String sub, View rightView, View.OnClickListener rowClick) {
        LinearLayout row = new LinearLayout(this);
        row.setOrientation(LinearLayout.HORIZONTAL);
        row.setGravity(Gravity.CENTER_VERTICAL);
        row.setPadding(dp(16), dp(14), dp(16), dp(14));
        if (rowClick != null) {
            row.setBackground(ripple(roundBox(Color.TRANSPARENT, 0, 0), 0));
            row.setOnClickListener(rowClick);
        }

        LinearLayout col = new LinearLayout(this);
        col.setOrientation(LinearLayout.VERTICAL);
        col.addView(label(title, 13, cOnSurface, true), new LinearLayout.LayoutParams(MATCH, WRAP));
        TextView subTv = label(sub, 12, cOnSurfaceVariant, false);
        LinearLayout.LayoutParams sLp = new LinearLayout.LayoutParams(MATCH, WRAP);
        sLp.topMargin = dp(2);
        col.addView(subTv, sLp);
        row.addView(col, new LinearLayout.LayoutParams(0, WRAP, 1f));

        LinearLayout.LayoutParams rLp = new LinearLayout.LayoutParams(WRAP, dp(30));
        rLp.leftMargin = dp(12);
        row.addView(rightView, rLp);
        return row;
    }

    private LinearLayout settingInfoRow(String key, TextView valView, View.OnClickListener click) {
        LinearLayout row = new LinearLayout(this);
        row.setOrientation(LinearLayout.HORIZONTAL);
        row.setGravity(Gravity.CENTER_VERTICAL);
        row.setPadding(dp(16), dp(13), dp(16), dp(13));
        if (click != null) {
            row.setBackground(ripple(roundBox(Color.TRANSPARENT, 0, 0), 0));
            row.setOnClickListener(click);
        }
        row.addView(label(key, 13, cOnSurfaceVariant, false), new LinearLayout.LayoutParams(0, WRAP, 1f));
        LinearLayout.LayoutParams vLp = new LinearLayout.LayoutParams(WRAP, WRAP);
        vLp.leftMargin = dp(12);
        row.addView(valView, vLp);
        return row;
    }

    private View hairline() {
        View v = new View(this);
        v.setBackgroundColor(cBorder);
        return v;
    }

    private void refreshSettingsInfo() {
        if (setNameVal != null) {
            setNameVal.setText(deviceName);
        }
        if (setIdVal != null && !fullDeviceId.isEmpty()) {
            String shortId = fullDeviceId.length() > 12 ? fullDeviceId.substring(0, 12) : fullDeviceId;
            setIdVal.setText(shortId + "（点击复制）");
        }
        if (setPortVal != null) {
            setPortVal.setText(recvRunning && !lanPort.isEmpty() ? lanPort : (recvRunning ? "启动中" : "未开启"));
        }
    }

    private void showRenameDialog() {
        final EditText input = modalInput("输入设备名称");
        input.setText(deviceName);
        input.setSelection(deviceName.length());

        FrameLayout wrap = new FrameLayout(this);
        wrap.setPadding(dp(20), dp(10), dp(20), 0);
        wrap.addView(input, new FrameLayout.LayoutParams(MATCH, dp(44)));

        new AlertDialog.Builder(this)
                .setTitle("修改设备名称")
                .setView(wrap)
                .setNegativeButton("取消", null)
                .setPositiveButton("保存", (d, w) -> {
                    String n = input.getText().toString().trim();
                    if (n.isEmpty()) {
                        n = defaultName();
                    }
                    deviceName = n;
                    getPreferences(MODE_PRIVATE).edit().putString("deviceName", n).apply();
                    refreshSettingsInfo();
                    toast("设备名称已更新，下次开启接收或配对生效");
                })
                .show();
    }

    // ================= Pairing Dialog (Android 12+ Blur + Camera2 QR + 10-min Timer) =================

    private void openPairSheet(int initialMode) {
        if (pairDialog != null && pairDialog.isShowing()) {
            switchPairMode(initialMode);
            return;
        }
        pairMode = initialMode;
        scanSucceeded.set(false);
        lastRejectedQR = "";

        Dialog dlg = new Dialog(this, android.R.style.Theme_Translucent_NoTitleBar);
        dlg.requestWindowFeature(Window.FEATURE_NO_TITLE);

        FrameLayout overlay = new FrameLayout(this);
        overlay.setBackgroundColor(Color.parseColor("#73000000"));
        overlay.setPadding(dp(16), dp(28), dp(16), dp(24));
        overlay.setOnClickListener(v -> closePairSheet());

        ScrollView sheetScroller = new ScrollView(this);
        sheetScroller.setFillViewport(false);
        FrameLayout.LayoutParams scLp = new FrameLayout.LayoutParams(MATCH, WRAP, Gravity.CENTER);
        scLp.width = Math.min(getResources().getDisplayMetrics().widthPixels - dp(32), dp(372));

        LinearLayout sheet = new LinearLayout(this);
        sheet.setOrientation(LinearLayout.VERTICAL);
        sheet.setPadding(dp(20), dp(18), dp(20), dp(20));
        sheet.setBackground(roundBox(cSheetBg, cBorder, 24));
        sheet.setClickable(true);

        // Close button top-right
        FrameLayout hdrWrap = new FrameLayout(this);
        TextView closeX = label("✕", 14, cOnSurfaceVariant, true);
        closeX.setGravity(Gravity.CENTER);
        closeX.setBackground(ripple(oval(Color.TRANSPARENT), 99));
        closeX.setOnClickListener(v -> closePairSheet());
        hdrWrap.addView(closeX, new FrameLayout.LayoutParams(dp(30), dp(30), Gravity.END | Gravity.CENTER_VERTICAL));
        sheet.addView(hdrWrap, new LinearLayout.LayoutParams(MATCH, WRAP));

        modeScanWrap = buildModeScan();
        modeShowWrap = buildModeShowQR();
        modeEnterWrap = buildModeEnterCode();

        sheet.addView(modeScanWrap, new LinearLayout.LayoutParams(MATCH, WRAP));
        sheet.addView(modeShowWrap, new LinearLayout.LayoutParams(MATCH, WRAP));
        sheet.addView(modeEnterWrap, new LinearLayout.LayoutParams(MATCH, WRAP));

        sheetScroller.addView(sheet, new FrameLayout.LayoutParams(MATCH, WRAP));
        overlay.addView(sheetScroller, scLp);

        dlg.setContentView(overlay);
        dlg.setCanceledOnTouchOutside(true);
        dlg.setOnDismissListener(d -> cleanupPairDialog());

        Window win = dlg.getWindow();
        if (win != null) {
            win.setBackgroundDrawable(new ColorDrawable(Color.TRANSPARENT));
            win.setLayout(MATCH, MATCH);
            win.setSoftInputMode(WindowManager.LayoutParams.SOFT_INPUT_ADJUST_RESIZE);
            WindowManager.LayoutParams lp = win.getAttributes();
            lp.dimAmount = 0.45f;
            win.addFlags(WindowManager.LayoutParams.FLAG_DIM_BEHIND);
            if (Build.VERSION.SDK_INT >= 31) {
                win.addFlags(WindowManager.LayoutParams.FLAG_BLUR_BEHIND);
                lp.setBlurBehindRadius(36);
                win.setBackgroundBlurRadius(36);
            }
            win.setAttributes(lp);
        }

        pairDialog = dlg;
        dlg.show();
        switchPairMode(initialMode);
    }

    private View buildModeScan() {
        LinearLayout col = new LinearLayout(this);
        col.setOrientation(LinearLayout.VERTICAL);
        col.setGravity(Gravity.CENTER_HORIZONTAL);

        TextView title = label("扫一扫配对", 17, cOnSurface, true);
        col.addView(title, new LinearLayout.LayoutParams(WRAP, WRAP));

        TextView sub = label("对准电脑或另一台手机轻渡显示的二维码", 12, cOnSurfaceVariant, false);
        LinearLayout.LayoutParams sLp = new LinearLayout.LayoutParams(WRAP, WRAP);
        sLp.topMargin = dp(4);
        sLp.bottomMargin = dp(16);
        col.addView(sub, sLp);

        // 224dp rounded square viewfinder
        FrameLayout vf = new FrameLayout(this);
        vf.setBackgroundColor(Color.parseColor("#020617"));
        vf.setOutlineProvider(new ViewOutlineProvider() {
            @Override
            public void getOutline(View view, Outline outline) {
                outline.setRoundRect(0, 0, view.getWidth(), view.getHeight(), dp(16));
            }
        });
        vf.setClipToOutline(true);

        scanTextureView = new TextureView(this);
        scanTextureView.setSurfaceTextureListener(new TextureView.SurfaceTextureListener() {
            @Override
            public void onSurfaceTextureAvailable(SurfaceTexture surface, int width, int height) {
                if (pairDialog != null && pairDialog.isShowing() && pairMode == 0) {
                    startCameraIfReady();
                }
            }

            @Override
            public void onSurfaceTextureSizeChanged(SurfaceTexture surface, int width, int height) {
                configureCameraTransform(width, height);
            }

            @Override
            public boolean onSurfaceTextureDestroyed(SurfaceTexture surface) {
                stopCamera();
                return true;
            }

            @Override
            public void onSurfaceTextureUpdated(SurfaceTexture surface) {
            }
        });
        vf.addView(scanTextureView, new FrameLayout.LayoutParams(MATCH, MATCH));

        CornerBracketsView brackets = new CornerBracketsView();
        vf.addView(brackets, new FrameLayout.LayoutParams(dp(146), dp(146), Gravity.CENTER));

        scanStatusLabel = label("相机取景中", 12, Color.parseColor("#E5E7EB"), false);
        scanStatusLabel.setGravity(Gravity.CENTER);
        scanStatusLabel.setPadding(dp(10), dp(4), dp(10), dp(4));
        scanStatusLabel.setBackground(roundBox(Color.parseColor("#88020617"), 0, 99));
        FrameLayout.LayoutParams stLp = new FrameLayout.LayoutParams(WRAP, WRAP, Gravity.BOTTOM | Gravity.CENTER_HORIZONTAL);
        stLp.bottomMargin = dp(12);
        vf.addView(scanStatusLabel, stLp);

        // Permission prompt overlay if camera not granted
        scanPermOverlay = new LinearLayout(this);
        scanPermOverlay.setOrientation(LinearLayout.VERTICAL);
        scanPermOverlay.setGravity(Gravity.CENTER);
        scanPermOverlay.setPadding(dp(16), dp(16), dp(16), dp(16));
        scanPermOverlay.setBackgroundColor(Color.parseColor("#E60F172A"));
        scanPermOverlay.setVisibility(View.GONE);

        TextView permTxt = label("需要相机权限以扫描二维码", 12, Color.WHITE, false);
        permTxt.setGravity(Gravity.CENTER);
        scanPermOverlay.addView(permTxt, new LinearLayout.LayoutParams(WRAP, WRAP));

        Button permBtn = smallFilledBtn("授权相机", cPrimaryPressed, cOnPrimary, v -> requestCameraPerm());
        LinearLayout.LayoutParams pbLp = new LinearLayout.LayoutParams(WRAP, dp(34));
        pbLp.topMargin = dp(10);
        scanPermOverlay.addView(permBtn, pbLp);
        vf.addView(scanPermOverlay, new FrameLayout.LayoutParams(MATCH, MATCH));

        LinearLayout.LayoutParams vfLp = new LinearLayout.LayoutParams(dp(224), dp(224));
        vfLp.bottomMargin = dp(18);
        col.addView(vf, vfLp);

        LinearLayout links = new LinearLayout(this);
        links.setOrientation(LinearLayout.HORIZONTAL);
        links.setPadding(0, dp(12), 0, 0);

        TextView toShow = label("显示我的二维码", 12, cPrimary, true);
        toShow.setOnClickListener(v -> switchPairMode(1));
        links.addView(toShow, new LinearLayout.LayoutParams(0, WRAP, 1f));

        TextView toEnter = label("输入 6 位配对码", 12, cPrimary, true);
        toEnter.setGravity(Gravity.END);
        toEnter.setOnClickListener(v -> switchPairMode(2));
        links.addView(toEnter, new LinearLayout.LayoutParams(0, WRAP, 1f));

        col.addView(hairline(), new LinearLayout.LayoutParams(MATCH, 1));
        col.addView(links, new LinearLayout.LayoutParams(MATCH, WRAP));
        return col;
    }

    private View buildModeShowQR() {
        LinearLayout col = new LinearLayout(this);
        col.setOrientation(LinearLayout.VERTICAL);
        col.setGravity(Gravity.CENTER_HORIZONTAL);
        col.setVisibility(View.GONE);

        TextView title = label("我的配对二维码", 17, cOnSurface, true);
        col.addView(title, new LinearLayout.LayoutParams(WRAP, WRAP));

        TextView sub = label("供另一台手机或电脑配对使用", 12, cOnSurfaceVariant, false);
        LinearLayout.LayoutParams sLp = new LinearLayout.LayoutParams(WRAP, WRAP);
        sLp.topMargin = dp(4);
        sLp.bottomMargin = dp(14);
        col.addView(sub, sLp);

        // White QR container so camera scan contrasts reliably in dark theme too
        FrameLayout qrBox = new FrameLayout(this);
        qrBox.setPadding(dp(12), dp(12), dp(12), dp(12));
        qrBox.setBackground(roundBox(Color.WHITE, Color.parseColor("#E5E7EB"), 16));

        showQrImage = new ImageView(this);
        showQrImage.setScaleType(ImageView.ScaleType.FIT_CENTER);
        qrBox.addView(showQrImage, new FrameLayout.LayoutParams(MATCH, MATCH));

        showExpiredMask = new LinearLayout(this);
        showExpiredMask.setOrientation(LinearLayout.VERTICAL);
        showExpiredMask.setGravity(Gravity.CENTER);
        showExpiredMask.setPadding(dp(12), dp(12), dp(12), dp(12));
        showExpiredMask.setBackground(roundBox(Color.parseColor("#F5FFFFFF"), 0, 14));
        showExpiredMask.setVisibility(View.GONE);

        TextView expTxt = label("已过期（超过 10 分钟）", 12, Color.parseColor("#DC2626"), true);
        expTxt.setGravity(Gravity.CENTER);
        showExpiredMask.addView(expTxt, new LinearLayout.LayoutParams(WRAP, WRAP));

        Button regenBtn = smallFilledBtn("重新生成", cPrimaryPressed, cOnPrimary, v -> refreshMyPairCode());
        LinearLayout.LayoutParams rgLp = new LinearLayout.LayoutParams(WRAP, dp(32));
        rgLp.topMargin = dp(10);
        showExpiredMask.addView(regenBtn, rgLp);
        qrBox.addView(showExpiredMask, new FrameLayout.LayoutParams(MATCH, MATCH));

        LinearLayout.LayoutParams qbLp = new LinearLayout.LayoutParams(dp(184), dp(184));
        qbLp.bottomMargin = dp(14);
        col.addView(qrBox, qbLp);

        // 6-digit code + copy
        LinearLayout codeRow = new LinearLayout(this);
        codeRow.setOrientation(LinearLayout.HORIZONTAL);
        codeRow.setGravity(Gravity.CENTER_VERTICAL);

        showCodeText = label("--- ---", 22, cOnSurface, true);
        showCodeText.setTypeface(Typeface.MONOSPACE);
        if (Build.VERSION.SDK_INT >= 21) {
            showCodeText.setLetterSpacing(0.14f);
        }
        codeRow.addView(showCodeText, new LinearLayout.LayoutParams(WRAP, WRAP));

        Button copyCodeBtn = smallOutlineBtn("复制", v -> {
            String raw = showCodeText.getText().toString().replace(" ", "");
            if (raw.length() == 6) {
                copyText("配对码", raw, "已复制 6 位配对码");
            }
        });
        LinearLayout.LayoutParams ccLp = new LinearLayout.LayoutParams(WRAP, dp(28));
        ccLp.leftMargin = dp(10);
        codeRow.addView(copyCodeBtn, ccLp);

        LinearLayout.LayoutParams crLp = new LinearLayout.LayoutParams(WRAP, WRAP);
        crLp.bottomMargin = dp(6);
        col.addView(codeRow, crLp);

        // 10-min countdown + refresh
        LinearLayout ttlRow = new LinearLayout(this);
        ttlRow.setOrientation(LinearLayout.HORIZONTAL);
        ttlRow.setGravity(Gravity.CENTER_VERTICAL);

        showTtlText = label("10 分钟内有效 · 剩余 10:00", 12, cOnSurfaceVariant, false);
        ttlRow.addView(showTtlText, new LinearLayout.LayoutParams(WRAP, WRAP));

        TextView midDot = label(" · ", 12, cOnSurfaceVariant, false);
        ttlRow.addView(midDot, new LinearLayout.LayoutParams(WRAP, WRAP));

        TextView refBtn = label("刷新", 12, cPrimary, true);
        refBtn.setPadding(dp(4), dp(2), dp(4), dp(2));
        refBtn.setOnClickListener(v -> refreshMyPairCode());
        ttlRow.addView(refBtn, new LinearLayout.LayoutParams(WRAP, WRAP));

        LinearLayout.LayoutParams trLp = new LinearLayout.LayoutParams(WRAP, WRAP);
        trLp.bottomMargin = dp(14);
        col.addView(ttlRow, trLp);

        col.addView(hairline(), new LinearLayout.LayoutParams(MATCH, 1));

        LinearLayout links = new LinearLayout(this);
        links.setOrientation(LinearLayout.HORIZONTAL);
        links.setPadding(0, dp(12), 0, 0);

        TextView toScan = label("返回扫一扫", 12, cPrimary, true);
        toScan.setOnClickListener(v -> switchPairMode(0));
        links.addView(toScan, new LinearLayout.LayoutParams(0, WRAP, 1f));

        TextView toEnter = label("输入 6 位配对码", 12, cPrimary, true);
        toEnter.setGravity(Gravity.END);
        toEnter.setOnClickListener(v -> switchPairMode(2));
        links.addView(toEnter, new LinearLayout.LayoutParams(0, WRAP, 1f));
        col.addView(links, new LinearLayout.LayoutParams(MATCH, WRAP));
        return col;
    }

    private View buildModeEnterCode() {
        LinearLayout col = new LinearLayout(this);
        col.setOrientation(LinearLayout.VERTICAL);
        col.setVisibility(View.GONE);

        TextView title = label("输入配对码", 17, cOnSurface, true);
        title.setGravity(Gravity.CENTER);
        col.addView(title, new LinearLayout.LayoutParams(MATCH, WRAP));

        TextView sub = label("输入电脑上显示的 6 位配对码（10 分钟内有效）", 12, cOnSurfaceVariant, false);
        sub.setGravity(Gravity.CENTER);
        LinearLayout.LayoutParams sLp = new LinearLayout.LayoutParams(MATCH, WRAP);
        sLp.topMargin = dp(4);
        sLp.bottomMargin = dp(14);
        col.addView(sub, sLp);

        TextView lblCode = label("6 位配对码", 12, cOnSurfaceVariant, false);
        col.addView(lblCode, new LinearLayout.LayoutParams(MATCH, WRAP));

        enterCodeField = modalInput("482917");
        enterCodeField.setInputType(InputType.TYPE_CLASS_NUMBER);
        enterCodeField.setFilters(new InputFilter[]{new InputFilter.LengthFilter(6)});
        enterCodeField.setTypeface(Typeface.MONOSPACE);
        if (Build.VERSION.SDK_INT >= 21) {
            enterCodeField.setLetterSpacing(0.15f);
        }
        LinearLayout.LayoutParams cfLp = new LinearLayout.LayoutParams(MATCH, dp(44));
        cfLp.topMargin = dp(4);
        cfLp.bottomMargin = dp(10);
        col.addView(enterCodeField, cfLp);

        TextView lblPeer = label("对方设备 ID（在电脑设置 → 本机信息中查看）", 12, cOnSurfaceVariant, false);
        col.addView(lblPeer, new LinearLayout.LayoutParams(MATCH, WRAP));

        enterPeerField = modalInput("粘贴 64 位设备 ID 或 ferry:// 链接");
        enterPeerField.setTypeface(Typeface.MONOSPACE);
        enterPeerField.setTextSize(12);
        LinearLayout.LayoutParams pfLp = new LinearLayout.LayoutParams(MATCH, dp(42));
        pfLp.topMargin = dp(4);
        pfLp.bottomMargin = dp(8);
        col.addView(enterPeerField, pfLp);

        enterStatusView = label("", 12, cError, false);
        enterStatusView.setVisibility(View.GONE);
        LinearLayout.LayoutParams esLp = new LinearLayout.LayoutParams(MATCH, WRAP);
        esLp.bottomMargin = dp(8);
        col.addView(enterStatusView, esLp);

        enterSubmitBtn = filledBtn("配对", cPrimaryPressed, cOnPrimary);
        enterSubmitBtn.setOnClickListener(v -> submitManualPair());
        LinearLayout.LayoutParams sbLp = new LinearLayout.LayoutParams(MATCH, dp(42));
        sbLp.bottomMargin = dp(14);
        col.addView(enterSubmitBtn, sbLp);

        col.addView(hairline(), new LinearLayout.LayoutParams(MATCH, 1));

        LinearLayout links = new LinearLayout(this);
        links.setOrientation(LinearLayout.HORIZONTAL);
        links.setPadding(0, dp(12), 0, 0);

        TextView toScan = label("返回扫一扫", 12, cPrimary, true);
        toScan.setOnClickListener(v -> switchPairMode(0));
        links.addView(toScan, new LinearLayout.LayoutParams(0, WRAP, 1f));

        TextView toShow = label("显示我的二维码", 12, cPrimary, true);
        toShow.setGravity(Gravity.END);
        toShow.setOnClickListener(v -> switchPairMode(1));
        links.addView(toShow, new LinearLayout.LayoutParams(0, WRAP, 1f));
        col.addView(links, new LinearLayout.LayoutParams(MATCH, WRAP));
        return col;
    }

    private void switchPairMode(int mode) {
        pairMode = mode;
        modeScanWrap.setVisibility(mode == 0 ? View.VISIBLE : View.GONE);
        modeShowWrap.setVisibility(mode == 1 ? View.VISIBLE : View.GONE);
        modeEnterWrap.setVisibility(mode == 2 ? View.VISIBLE : View.GONE);

        if (mode == 0) {
            scanSucceeded.set(false);
            if (scanStatusLabel != null) {
                scanStatusLabel.setText("相机取景中");
            }
            if (hasCameraPerm()) {
                scanPermOverlay.setVisibility(View.GONE);
                startCameraIfReady();
            } else {
                scanPermOverlay.setVisibility(View.VISIBLE);
                requestCameraPerm();
            }
        } else {
            stopCamera();
        }

        if (mode == 1) {
            refreshMyPairCode();
        } else {
            cancelCountdown();
            pairShowSeq++;
        }
    }

    private void closePairSheet() {
        Dialog dlg = pairDialog;
        pairDialog = null;
        if (dlg != null && dlg.isShowing()) {
            dlg.dismiss();
        } else {
            cleanupPairDialog();
        }
    }

    private void cleanupPairDialog() {
        pairDialog = null;
        stopCamera();
        cancelCountdown();
        pairShowSeq++;
    }

    private void cancelCountdown() {
        if (pairCountdown != null) {
            pairCountdown.cancel();
            pairCountdown = null;
        }
    }

    private void refreshMyPairCode() {
        cancelCountdown();
        final int seq = ++pairShowSeq;
        showExpiredMask.setVisibility(View.GONE);
        showTtlText.setText("10 分钟内有效 · 剩余 10:00");
        showTtlText.setTextColor(cOnSurfaceVariant);

        final String code = Mobile.generatePairCode();
        showCodeText.setText(code.length() == 6
                ? code.substring(0, 3) + " " + code.substring(3)
                : code);

        ioPool.execute(() -> {
            try {
                String uri = Mobile.buildMyPairURI(keyFile().getAbsolutePath(), code, deviceName, PAIR_TTL_SEC);
                byte[] png = Mobile.generateQRPNG(uri, 512);
                final Bitmap bmp = BitmapFactory.decodeByteArray(png, 0, png.length);
                ui.post(() -> {
                    if (seq != pairShowSeq || pairDialog == null || !pairDialog.isShowing()) {
                        return;
                    }
                    if (bmp != null) {
                        showQrImage.setImageBitmap(bmp);
                    }
                    startShowCountdown(seq);
                });
            } catch (Exception e) {
                Log.w(TAG, "buildMyPairURI/QR", e);
                ui.post(() -> toast("生成二维码失败：" + e.getMessage()));
            }
        });

        new Thread(() -> {
            try {
                Mobile.waitPairCode(SIGNAL_URL, keyFile().getAbsolutePath(), pairedFile().getAbsolutePath(),
                        code, PAIR_TTL_SEC * 1000L);
                ui.post(() -> {
                    if (seq != pairShowSeq || pairDialog == null || !pairDialog.isShowing()) {
                        return;
                    }
                    toast("配对成功");
                    loadPairedPeers();
                    closePairSheet();
                });
            } catch (Exception e) {
                Log.w(TAG, "waitPairCode", e);
                ui.post(() -> {
                    if (seq != pairShowSeq || pairDialog == null || !pairDialog.isShowing()) {
                        return;
                    }
                    String msg = e.getMessage();
                    if (msg != null && !msg.trim().isEmpty()) {
                        showTtlText.setText(msg);
                        showTtlText.setTextColor(cError);
                    }
                });
            }
        }).start();
    }

    private void startShowCountdown(final int seq) {
        cancelCountdown();
        pairCountdown = new CountDownTimer(PAIR_TTL_SEC * 1000L, 1000L) {
            @Override
            public void onTick(long msLeft) {
                if (seq != pairShowSeq || pairDialog == null || !pairDialog.isShowing()) {
                    cancel();
                    return;
                }
                long sec = Math.max(0, msLeft / 1000L);
                showTtlText.setText(String.format(Locale.US, "10 分钟内有效 · 剩余 %02d:%02d", sec / 60, sec % 60));
            }

            @Override
            public void onFinish() {
                if (seq != pairShowSeq || pairDialog == null || !pairDialog.isShowing()) {
                    return;
                }
                showTtlText.setText("已过期（超过 10 分钟）");
                showTtlText.setTextColor(cError);
                showExpiredMask.setVisibility(View.VISIBLE);
            }
        }.start();
    }

    private void submitManualPair() {
        String code = enterCodeField.getText().toString().trim();
        String peer = enterPeerField.getText().toString().trim();
        if (peer.startsWith("ferry://")) {
            enterSubmitBtn.setEnabled(false);
            enterStatusView.setVisibility(View.VISIBLE);
            enterStatusView.setTextColor(cOnSurfaceVariant);
            enterStatusView.setText("正在验证二维码链接并配对…");
            new Thread(() -> {
                try {
                    Mobile.pairWithURI(SIGNAL_URL, keyFile().getAbsolutePath(), pairedFile().getAbsolutePath(), peer);
                    ui.post(() -> {
                        if (pairDialog != null && pairDialog.isShowing()) {
                            toast("配对成功");
                            loadPairedPeers();
                            closePairSheet();
                        }
                    });
                } catch (Exception e) {
                    ui.post(() -> {
                        enterSubmitBtn.setEnabled(true);
                        enterStatusView.setTextColor(cError);
                        enterStatusView.setText("配对失败：" + e.getMessage());
                    });
                }
            }).start();
            return;
        }
        if (code.length() != 6) {
            enterStatusView.setVisibility(View.VISIBLE);
            enterStatusView.setTextColor(cError);
            enterStatusView.setText("请输入 6 位数字配对码");
            return;
        }
        if (peer.isEmpty()) {
            enterStatusView.setVisibility(View.VISIBLE);
            enterStatusView.setTextColor(cError);
            enterStatusView.setText("请粘贴对方设备 ID（在电脑设置 → 本机信息查看），或直接使用扫一扫");
            return;
        }
        enterSubmitBtn.setEnabled(false);
        enterStatusView.setVisibility(View.VISIBLE);
        enterStatusView.setTextColor(cOnSurfaceVariant);
        enterStatusView.setText("正在协商信任密钥…");
        new Thread(() -> {
            try {
                Mobile.pairWithCode(SIGNAL_URL, keyFile().getAbsolutePath(), pairedFile().getAbsolutePath(), code, peer);
                ui.post(() -> {
                    if (pairDialog != null && pairDialog.isShowing()) {
                        toast("配对成功");
                        loadPairedPeers();
                        closePairSheet();
                    }
                });
            } catch (Exception e) {
                ui.post(() -> {
                    enterSubmitBtn.setEnabled(true);
                    enterStatusView.setTextColor(cError);
                    enterStatusView.setText("配对失败：" + e.getMessage());
                });
            }
        }).start();
    }

    // ================= Camera2 QR Scanner =================

    private boolean hasCameraPerm() {
        if (Build.VERSION.SDK_INT < 23) {
            return true;
        }
        return checkSelfPermission(Manifest.permission.CAMERA) == PackageManager.PERMISSION_GRANTED;
    }

    private void requestCameraPerm() {
        if (Build.VERSION.SDK_INT >= 23 && !hasCameraPerm()) {
            requestPermissions(new String[]{Manifest.permission.CAMERA}, REQ_CAMERA);
        }
    }

    @Override
    public void onRequestPermissionsResult(int requestCode, String[] permissions, int[] grantResults) {
        super.onRequestPermissionsResult(requestCode, permissions, grantResults);
        if (requestCode == REQ_CAMERA) {
            boolean granted = grantResults != null && grantResults.length > 0
                    && grantResults[0] == PackageManager.PERMISSION_GRANTED;
            if (pairDialog != null && pairDialog.isShowing() && pairMode == 0) {
                if (granted) {
                    if (scanPermOverlay != null) {
                        scanPermOverlay.setVisibility(View.GONE);
                    }
                    startCameraIfReady();
                } else if (scanStatusLabel != null) {
                    scanStatusLabel.setText("未授予定位/相机权限，请点授权相机");
                }
            }
        }
    }

    private void startCameraIfReady() {
        if (!hasCameraPerm() || scanTextureView == null || !scanTextureView.isAvailable()) {
            return;
        }
        if (cameraWanted || cameraDevice != null) {
            return;
        }
        cameraWanted = true;
        if (scanStatusLabel != null) {
            scanStatusLabel.setText("相机取景中");
        }
        camThread = new HandlerThread("qr-cam");
        camThread.start();
        camHandler = new Handler(camThread.getLooper());

        try {
            CameraManager cm = (CameraManager) getSystemService(CAMERA_SERVICE);
            if (cm == null) {
                stopCamera();
                return;
            }
            String camId = chooseBackCamera(cm);
            if (camId == null) {
                if (scanStatusLabel != null) {
                    scanStatusLabel.setText("未检测到可用相机");
                }
                stopCamera();
                return;
            }
            CameraCharacteristics cc = cm.getCameraCharacteristics(camId);
            StreamConfigurationMap map = cc.get(CameraCharacteristics.SCALER_STREAM_CONFIGURATION_MAP);
            previewSize = chooseScanSize(map);
            configureCameraTransform(scanTextureView.getWidth(), scanTextureView.getHeight());

            imageReader = ImageReader.newInstance(previewSize.getWidth(), previewSize.getHeight(),
                    ImageFormat.YUV_420_888, 2);
            imageReader.setOnImageAvailableListener(this::onCameraFrameAvailable, camHandler);

            cm.openCamera(camId, new CameraDevice.StateCallback() {
                @Override
                public void onOpened(CameraDevice cam) {
                    if (!cameraWanted || pairDialog == null || !pairDialog.isShowing() || pairMode != 0) {
                        cam.close();
                        return;
                    }
                    cameraDevice = cam;
                    createScanSession(cam);
                }

                @Override
                public void onDisconnected(CameraDevice cam) {
                    cam.close();
                    if (cameraDevice == cam) {
                        cameraDevice = null;
                    }
                }

                @Override
                public void onError(CameraDevice cam, int error) {
                    cam.close();
                    if (cameraDevice == cam) {
                        cameraDevice = null;
                    }
                    ui.post(() -> {
                        if (scanStatusLabel != null) {
                            scanStatusLabel.setText("相机打开失败 (" + error + ")");
                        }
                    });
                }
            }, camHandler);
        } catch (SecurityException se) {
            Log.w(TAG, "camera perm", se);
            stopCamera();
        } catch (Exception e) {
            Log.e(TAG, "startCamera", e);
            stopCamera();
            if (scanStatusLabel != null) {
                scanStatusLabel.setText("相机启动失败");
            }
        }
    }

    private void createScanSession(final CameraDevice cam) {
        try {
            SurfaceTexture st = scanTextureView.getSurfaceTexture();
            if (st == null || imageReader == null) {
                stopCamera();
                return;
            }
            st.setDefaultBufferSize(previewSize.getWidth(), previewSize.getHeight());
            final Surface previewSurface = new Surface(st);
            final Surface readerSurface = imageReader.getSurface();
            final CaptureRequest.Builder req = cam.createCaptureRequest(CameraDevice.TEMPLATE_PREVIEW);
            req.addTarget(previewSurface);
            req.addTarget(readerSurface);
            req.set(CaptureRequest.CONTROL_AF_MODE, CaptureRequest.CONTROL_AF_MODE_CONTINUOUS_PICTURE);

            cam.createCaptureSession(Arrays.asList(previewSurface, readerSurface),
                    new CameraCaptureSession.StateCallback() {
                        @Override
                        public void onConfigured(CameraCaptureSession session) {
                            if (!cameraWanted || cameraDevice != cam || pairDialog == null || !pairDialog.isShowing()) {
                                try {
                                    session.close();
                                } catch (Exception ignored) {
                                }
                                return;
                            }
                            captureSession = session;
                            try {
                                session.setRepeatingRequest(req.build(), null, camHandler);
                            } catch (Exception e) {
                                Log.w(TAG, "repeatingRequest", e);
                            }
                        }

                        @Override
                        public void onConfigureFailed(CameraCaptureSession session) {
                            ui.post(() -> {
                                if (scanStatusLabel != null) {
                                    scanStatusLabel.setText("相机预览配置失败");
                                }
                            });
                        }
                    }, camHandler);
        } catch (Exception e) {
            Log.e(TAG, "createScanSession", e);
            stopCamera();
        }
    }

    private void onCameraFrameAvailable(ImageReader reader) {
        Image img = reader.acquireLatestImage();
        if (img == null) {
            return;
        }
        byte[] gray = null;
        int w = 0;
        int h = 0;
        boolean acquired = false;
        try {
            if (!cameraWanted || scanSucceeded.get()) {
                return;
            }
            long now = SystemClock.uptimeMillis();
            if (now - lastScanTickMs < 350 || !decodingFrame.compareAndSet(false, true)) {
                return;
            }
            acquired = true;
            lastScanTickMs = now;
            w = img.getWidth();
            h = img.getHeight();
            Image.Plane yPlane = img.getPlanes()[0];
            int rowStride = yPlane.getRowStride();
            int pixelStride = yPlane.getPixelStride();
            ByteBuffer buf = yPlane.getBuffer();
            gray = new byte[w * h];
            if (pixelStride == 1 && rowStride == w) {
                buf.get(gray, 0, w * h);
            } else {
                byte[] row = new byte[rowStride];
                for (int r = 0; r < h; r++) {
                    buf.position(r * rowStride);
                    int rem = Math.min(rowStride, buf.remaining());
                    buf.get(row, 0, rem);
                    if (pixelStride == 1) {
                        System.arraycopy(row, 0, gray, r * w, Math.min(w, rem));
                    } else {
                        for (int c = 0; c < w; c++) {
                            int idx = c * pixelStride;
                            gray[r * w + c] = idx < rem ? row[idx] : 0;
                        }
                    }
                }
            }
        } catch (Exception e) {
            Log.w(TAG, "cam frame", e);
            if (acquired) {
                decodingFrame.set(false);
            }
            gray = null;
        } finally {
            img.close();
        }
        if (gray == null) {
            return;
        }
        final byte[] frameGray = gray;
        final int fw = w;
        final int fh = h;
        ioPool.execute(() -> {
            try {
                String text = Mobile.decodeQRGray(frameGray, fw, fh);
                if (text != null) {
                    final String payload = text.trim();
                    if (!payload.isEmpty() && (payload.startsWith("ferry://") || payload.startsWith("ftpair://"))) {
                        long now = SystemClock.uptimeMillis();
                        if (payload.equals(lastRejectedQR) && now - lastRejectMs < 3000) {
                            return;
                        }
                        if (scanSucceeded.compareAndSet(false, true)) {
                            ui.post(() -> handleScannedQR(payload));
                        }
                    }
                }
            } catch (Exception e) {
                Log.w(TAG, "decodeQRGray", e);
            } finally {
                decodingFrame.set(false);
            }
        });
    }

    private void handleScannedQR(final String payload) {
        stopCamera();
        if (scanStatusLabel != null) {
            scanStatusLabel.setText("已识别二维码，正在配对…");
        }
        new Thread(() -> {
            try {
                if (payload.startsWith("ftpair://")) {
                    Mobile.pairImport(payload, pairedFile().getAbsolutePath());
                } else {
                    Mobile.pairWithURI(SIGNAL_URL, keyFile().getAbsolutePath(), pairedFile().getAbsolutePath(), payload);
                }
                ui.post(() -> {
                    if (pairDialog != null && pairDialog.isShowing()) {
                        toast("配对成功");
                        loadPairedPeers();
                        closePairSheet();
                    }
                });
            } catch (Exception e) {
                ui.post(() -> {
                    lastRejectedQR = payload;
                    lastRejectMs = SystemClock.uptimeMillis();
                    scanSucceeded.set(false);
                    String errMsg = e.getMessage() == null ? "配对失败" : e.getMessage();
                    if (scanStatusLabel != null) {
                        scanStatusLabel.setText(errMsg);
                    }
                    toast(errMsg);
                    if (pairDialog != null && pairDialog.isShowing() && pairMode == 0) {
                        startCameraIfReady();
                    }
                });
            }
        }).start();
    }

    private void stopCamera() {
        cameraWanted = false;
        if (captureSession != null) {
            try {
                captureSession.close();
            } catch (Exception ignored) {
            }
            captureSession = null;
        }
        if (cameraDevice != null) {
            try {
                cameraDevice.close();
            } catch (Exception ignored) {
            }
            cameraDevice = null;
        }
        if (imageReader != null) {
            try {
                imageReader.close();
            } catch (Exception ignored) {
            }
            imageReader = null;
        }
        if (camThread != null) {
            camThread.quitSafely();
            camThread = null;
            camHandler = null;
        }
    }

    private static String chooseBackCamera(CameraManager cm) throws Exception {
        String[] ids = cm.getCameraIdList();
        for (String id : ids) {
            CameraCharacteristics cc = cm.getCameraCharacteristics(id);
            Integer facing = cc.get(CameraCharacteristics.LENS_FACING);
            if (facing != null && facing == CameraCharacteristics.LENS_FACING_BACK) {
                return id;
            }
        }
        return ids.length > 0 ? ids[0] : null;
    }

    private static Size chooseScanSize(StreamConfigurationMap map) {
        if (map == null) {
            return new Size(960, 720);
        }
        Size[] sizes = map.getOutputSizes(ImageFormat.YUV_420_888);
        if (sizes == null || sizes.length == 0) {
            return new Size(960, 720);
        }
        Size best = sizes[0];
        int bestDiff = Integer.MAX_VALUE;
        int targetArea = 960 * 720;
        for (Size s : sizes) {
            if (s.getWidth() > 1440 || s.getHeight() > 1080) {
                continue;
            }
            int diff = Math.abs(s.getWidth() * s.getHeight() - targetArea);
            if (diff < bestDiff) {
                bestDiff = diff;
                best = s;
            }
        }
        return best;
    }

    private void configureCameraTransform(int viewW, int viewH) {
        if (scanTextureView == null || viewW <= 0 || viewH <= 0) {
            return;
        }
        int rotation = getWindowManager().getDefaultDisplay().getRotation();
        float bw = previewSize.getHeight();
        float bh = previewSize.getWidth();
        if (rotation == Surface.ROTATION_90 || rotation == Surface.ROTATION_270) {
            bw = previewSize.getWidth();
            bh = previewSize.getHeight();
        }
        float scale = Math.max(viewW / bw, viewH / bh);
        float sx = (bw * scale) / viewW;
        float sy = (bh * scale) / viewH;
        Matrix m = new Matrix();
        float cx = viewW / 2f;
        float cy = viewH / 2f;
        m.setScale(sx, sy, cx, cy);
        if (rotation == Surface.ROTATION_90) {
            m.postRotate(270f, cx, cy);
        } else if (rotation == Surface.ROTATION_270) {
            m.postRotate(90f, cx, cy);
        } else if (rotation == Surface.ROTATION_180) {
            m.postRotate(180f, cx, cy);
        }
        scanTextureView.setTransform(m);
    }

    // ================= Receive & Web Share Controls =================

    private void toggleReceive() {
        if (recvRunning) {
            getPreferences(MODE_PRIVATE).edit().putBoolean("recvAuto", false).apply();
            stopAllReceive();
        } else {
            getPreferences(MODE_PRIVATE).edit().putBoolean("recvAuto", true).apply();
            startAllReceive();
        }
    }

    private void startAllReceive() {
        if (setRecvBtn != null) {
            setRecvBtn.setEnabled(false);
        }
        final String name = deviceName;
        new Thread(() -> {
            String lanAddr = "";
            String autoErr = "";
            try {
                lanAddr = Mobile.startReceiver(name, inboxDir().getAbsolutePath(), lanHint());
            } catch (Exception e) {
                Log.e(TAG, "startReceiver", e);
            }
            try {
                Mobile.startAutoReceive(name, inboxDir().getAbsolutePath(), SIGNAL_URL,
                        keyFile().getAbsolutePath(), pairedFile().getAbsolutePath(), lanHint(), "");
            } catch (Exception e) {
                Log.e(TAG, "startAutoReceive", e);
                autoErr = e.getMessage();
            }
            final String addr = lanAddr;
            final String err = autoErr;
            ui.post(() -> {
                if (setRecvBtn != null) {
                    setRecvBtn.setEnabled(true);
                }
                if (addr.isEmpty() && err != null && !err.isEmpty()) {
                    toast("开启接收失败：" + err);
                    return;
                }
                recvRunning = true;
                lanPort = extractPort(addr);
                acquireMulticastLock();
                startKeepAlive(lanPort.isEmpty() ? "正在等待接收文件" : "接收中 · 端口 " + lanPort);
                refreshReceiveUi();
                refreshSettingsInfo();
                ui.removeCallbacks(pollTask);
                ui.postDelayed(pollTask, 1000);
            });
        }).start();
    }

    private void stopAllReceive() {
        if (setRecvBtn != null) {
            setRecvBtn.setEnabled(false);
        }
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
                if (setRecvBtn != null) {
                    setRecvBtn.setEnabled(true);
                }
                recvRunning = false;
                lanPort = "";
                releaseMulticastLock();
                if (!webRunning) {
                    stopKeepAlive();
                }
                refreshReceiveUi();
                refreshSettingsInfo();
                toast("已关闭后台接收");
            });
        }).start();
    }

    private void toggleWebShare() {
        if (setWebBtn != null) {
            setWebBtn.setEnabled(false);
        }
        if (webRunning) {
            new Thread(() -> {
                try {
                    Mobile.stopWebShare();
                } catch (Exception e) {
                    Log.w(TAG, "stopWebShare", e);
                }
                ui.post(() -> {
                    if (setWebBtn != null) {
                        setWebBtn.setEnabled(true);
                    }
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
                    if (setWebBtn != null) {
                        setWebBtn.setEnabled(true);
                    }
                    webRunning = true;
                    webShareUrl = shown;
                    startKeepAlive("网页快传中");
                    refreshReceiveUi();
                });
            } catch (Exception e) {
                Log.e(TAG, "startWebShare", e);
                ui.post(() -> {
                    if (setWebBtn != null) {
                        setWebBtn.setEnabled(true);
                    }
                    toast("网页快传启动失败：" + e.getMessage());
                });
            }
        }).start();
    }

    private void refreshReceiveUi() {
        if (sendRecvStatusBtn != null) {
            sendRecvStatusBtn.setText(recvRunning ? "已开启接收" : "未开启接收");
            sendRecvStatusBtn.setTextColor(recvRunning ? cPrimary : cOnSurfaceVariant);
        }
        if (recvPulseDot != null) {
            recvPulseDot.setBackground(oval(recvRunning ? cPrimary : cOutline));
        }
        if (recvRunning) {
            startDotPulse();
        } else {
            stopDotPulse();
        }
        if (setRecvBtn != null) {
            setRecvBtn.setText(recvRunning ? "已开启" : "已关闭");
            setRecvBtn.setBackground(ripple(roundBox(
                    recvRunning ? cPrimaryContainer : cSurface,
                    recvRunning ? cPrimary : cBorder, 10), 10));
            setRecvBtn.setTextColor(recvRunning ? cOnPrimaryContainer : cOnSurfaceVariant);
        }
        if (setWebBtn != null) {
            setWebBtn.setText(webRunning ? "已开启" : "开启");
            setWebBtn.setBackground(ripple(roundBox(
                    webRunning ? cPrimaryContainer : cSurface,
                    webRunning ? cPrimary : cBorder, 10), 10));
            setWebBtn.setTextColor(webRunning ? cOnPrimaryContainer : cOnSurface);
        }
        if (setWebUrlView != null) {
            if (webRunning && !webShareUrl.isEmpty()) {
                setWebUrlView.setText("同一 Wi-Fi 浏览器打开（点击复制）：" + webShareUrl);
                setWebUrlView.setVisibility(View.VISIBLE);
            } else {
                setWebUrlView.setVisibility(View.GONE);
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

    // ================= Receive Poll -> Public Download/Ferry =================

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
        final ReceivedEntry entry = new ReceivedEntry(fileName, savedUri, mime, via, bytes, ts, "recv", "");
        ui.post(() -> {
            addRecentEntry(entry);
            toast("已保存到「下载/Ferry」：" + fileName);
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

    // ================= Recent Records State & Filtering =================

    private void addRecentEntry(ReceivedEntry entry) {
        recentEntries.add(0, entry);
        while (recentEntries.size() > 30) {
            recentEntries.remove(recentEntries.size() - 1);
        }
        persistRecentState();
        renderRecentList();
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
                            o.optLong("ts", 0),
                            o.optString("dir", "recv"),
                            o.optString("peer", "")));
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
                    o.put("dir", r.dir);
                    o.put("peer", r.peer);
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

    private void renderFilterTabs() {
        String[] fKeys = {"all", "recv", "send"};
        for (int i = 0; i < 3; i++) {
            TextView tv = filterTabs[i];
            if (tv == null) {
                continue;
            }
            boolean sel = fKeys[i].equals(activeFilter);
            tv.setTextColor(sel ? cPrimary : cOnSurfaceVariant);
            tv.setAlpha(sel ? 1f : 0.72f);
        }
    }

    private void renderRecentList() {
        if (recentListView == null || recentEmptyView == null) {
            return;
        }
        recentListView.removeAllViews();
        List<ReceivedEntry> visible = new ArrayList<>();
        for (ReceivedEntry r : recentEntries) {
            if ("all".equals(activeFilter) || activeFilter.equals(r.dir)) {
                visible.add(r);
            }
        }
        if (visible.isEmpty()) {
            recentListView.setVisibility(View.GONE);
            recentEmptyView.setVisibility(View.VISIBLE);
            return;
        }
        recentListView.setVisibility(View.VISIBLE);
        recentEmptyView.setVisibility(View.GONE);

        SimpleDateFormat sdf = new SimpleDateFormat("HH:mm", Locale.US);
        for (int i = 0; i < visible.size(); i++) {
            final ReceivedEntry r = visible.get(i);
            final boolean isSend = "send".equals(r.dir);

            LinearLayout row = new LinearLayout(this);
            row.setOrientation(LinearLayout.HORIZONTAL);
            row.setGravity(Gravity.CENTER_VERTICAL);
            row.setPadding(dp(2), dp(12), dp(2), dp(12));
            row.setBackground(ripple(roundBox(Color.TRANSPARENT, 0, 10), 10));
            if (!isSend && r.uri != null && !r.uri.isEmpty()) {
                row.setOnClickListener(v -> openReceivedEntry(r));
            } else if (isSend) {
                row.setOnClickListener(v -> toast("已发送：" + r.name));
            }

            LinearLayout col = new LinearLayout(this);
            col.setOrientation(LinearLayout.VERTICAL);

            TextView nm = label(r.name, 13, cOnSurface, true);
            nm.setSingleLine(true);
            nm.setEllipsize(TextUtils.TruncateAt.MIDDLE);
            col.addView(nm, new LinearLayout.LayoutParams(MATCH, WRAP));

            String dirPrefix = isSend
                    ? (r.peer == null || r.peer.isEmpty() ? "已发出" : "发往 " + r.peer)
                    : (r.peer == null || r.peer.isEmpty() ? "已收到" : "来自 " + r.peer);
            String timeStr = r.ts > 0 ? " · " + sdf.format(new Date(r.ts)) : "";
            TextView mt = label(dirPrefix + " · " + fmtBytes(r.bytes) + timeStr, 12, cOnSurfaceVariant, false);
            LinearLayout.LayoutParams mtLp = new LinearLayout.LayoutParams(MATCH, WRAP);
            mtLp.topMargin = dp(2);
            col.addView(mt, mtLp);
            row.addView(col, new LinearLayout.LayoutParams(0, WRAP, 1f));

            TextView actionHint = label(isSend ? "已发" : "打开", 12, isSend ? cOnSurfaceVariant : cPrimary, false);
            LinearLayout.LayoutParams ahLp = new LinearLayout.LayoutParams(WRAP, WRAP);
            ahLp.leftMargin = dp(12);
            row.addView(actionHint, ahLp);

            recentListView.addView(row, new LinearLayout.LayoutParams(MATCH, WRAP));
            if (i < visible.size() - 1) {
                recentListView.addView(hairline(), new LinearLayout.LayoutParams(MATCH, 1));
            }
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
        showPage(0);
        if (shareBannerView != null) {
            shareBannerView.setText("已从其他应用带入 " + uris.size() + " 个文件 · 点「发文件 / 照片」发送");
            shareBannerView.setVisibility(View.VISIBLE);
        }
    }

    private void pickFiles() {
        final String target = selectedTarget;
        if (!isAutoTarget(target) && !isLanTarget(target)) {
            toast("请先选择或配对接收设备");
            return;
        }
        saveLastPeer(target);
        final String targetName = resolveTargetName(target);
        if (sharedUris != null && !sharedUris.isEmpty()) {
            List<Uri> uris = sharedUris;
            sharedUris = null;
            if (shareBannerView != null) {
                shareBannerView.setVisibility(View.GONE);
            }
            sendUriBatch(uris, deviceName, target, targetName);
            return;
        }
        Intent i = new Intent(Intent.ACTION_OPEN_DOCUMENT);
        i.addCategory(Intent.CATEGORY_OPENABLE);
        i.setType("*/*");
        i.putExtra(Intent.EXTRA_ALLOW_MULTIPLE, true);
        startActivityForResult(i, PICK_FILES);
    }

    private void sendClipboard() {
        final String target = selectedTarget;
        if (!isAutoTarget(target) && !isLanTarget(target)) {
            toast("请先选择或配对接收设备");
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
        final String name = deviceName;
        final String targetName = resolveTargetName(target);
        final boolean auto = isAutoTarget(target);
        setSendBusy(true, "正在发送剪贴板文本…");
        new Thread(() -> {
            File dir = new File(getCacheDir(), "clip-" + System.nanoTime());
            dir.mkdirs();
            String ts = new SimpleDateFormat("HHmmss", Locale.US).format(new Date());
            File f = new File(dir, "clipboard-" + ts + ".txt");
            try (OutputStream out = new FileOutputStream(f)) {
                out.write(text.getBytes("UTF-8"));
                List<File> list = new ArrayList<>();
                list.add(f);
                sendPreparedFiles(list, dir, name, target, targetName, auto);
            } catch (Exception e) {
                Log.e(TAG, "sendClipboard", e);
                deleteTree(f, dir);
                onSendFailed(e.getMessage());
            }
        }).start();
    }

    private void sendUriBatch(final List<Uri> uris, final String name, final String target, final String targetName) {
        final boolean auto = isAutoTarget(target);
        setSendBusy(true, "正在发送 " + uris.size() + " 个文件…");
        new Thread(() -> {
            File batchDir = new File(new File(getCacheDir(), "outgoing"), String.valueOf(System.nanoTime()));
            List<File> tempFiles = new ArrayList<>();
            try {
                for (int i = 0; i < uris.size(); i++) {
                    tempFiles.add(copyToCache(uris.get(i), new File(batchDir, String.valueOf(i))));
                }
                sendPreparedFiles(tempFiles, batchDir, name, target, targetName, auto);
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

    private void sendPreparedFiles(List<File> files, File rootTmp, String name, String target,
                                   String targetName, boolean auto) {
        try {
            JSONArray paths = new JSONArray();
            for (File f : files) {
                paths.put(f.getAbsolutePath());
            }
            final String summary = auto
                    ? Mobile.sendAuto(SIGNAL_URL, keyFile().getAbsolutePath(),
                            pairedFile().getAbsolutePath(), target, name, paths.toString(), lanHint(), "")
                    : Mobile.sendFiles(target, name, paths.toString());
            onSendSucceeded(summary, targetName);
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
        if (sendPickTile != null) {
            sendPickTile.setEnabled(!busy);
            sendPickTile.setAlpha(busy ? 0.6f : 1f);
        }
        if (sendClipTile != null) {
            sendClipTile.setEnabled(!busy);
            sendClipTile.setAlpha(busy ? 0.6f : 1f);
        }
        if (busy) {
            startKeepAlive(msg);
            showSendBanner("", msg, false);
        }
    }

    private void onSendSucceeded(final String raw, final String targetName) {
        ui.post(() -> {
            setSendBusy(false, "");
            if (recvRunning || webRunning) {
                startKeepAlive(recvRunning ? "接收中" : "网页快传中");
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
                String first = n > 0 ? files.optString(0, "已发送文件") : "已发送文件";
                String recTitle = n > 1 ? first + " 等 " + n + " 个文件" : first;
                addRecentEntry(new ReceivedEntry(recTitle, "", "", path, bytes,
                        System.currentTimeMillis(), "send", targetName));
                showSendBanner(path, "已发送 · " + recTitle + " (" + fmtBytes(bytes) + ")", false);
            } catch (Exception e) {
                showSendBanner("", raw, false);
            }
        });
    }

    private void onSendFailed(final String err) {
        ui.post(() -> {
            setSendBusy(false, "");
            if (recvRunning || webRunning) {
                startKeepAlive(recvRunning ? "接收中" : "网页快传中");
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
        sendResultCard.setBackground(roundBox(isError ? cErrorContainer : cSurfaceAlt, 0, 10));
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
            sendUriBatch(uris, deviceName, selectedTarget, resolveTargetName(selectedTarget));
        }
    }

    // ================= Devices, Discovery & Legacy Import =================

    private void loadDeviceId() {
        ioPool.execute(() -> {
            try {
                String id = Mobile.deviceIDFor(keyFile().getAbsolutePath());
                final String full = id == null ? "" : id;
                ui.post(() -> {
                    fullDeviceId = full;
                    refreshSettingsInfo();
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
                        name = "设备 " + (id.length() > 6 ? id.substring(0, 6) : id);
                    }
                    entries.add(new PeerEntry(name, id));
                }
                ui.post(() -> {
                    pairedEntries.clear();
                    pairedEntries.addAll(entries);
                    if (selectedTarget.isEmpty() && !entries.isEmpty()) {
                        selectedTarget = entries.get(0).value;
                    }
                    renderPeerSection();
                });
            } catch (Exception e) {
                Log.w(TAG, "loadPairedPeers", e);
            }
        });
    }

    private void discover() {
        if (lanSearching) {
            return;
        }
        lanSearching = true;
        renderPeerSection();
        final String name = deviceName;
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
                        dn = addr;
                    }
                    found.add(new PeerEntry(dn + " (LAN)", addr));
                }
                ui.post(() -> {
                    lanSearching = false;
                    lanEntries.clear();
                    lanEntries.addAll(found);
                    if (!found.isEmpty() && selectedTarget.isEmpty()) {
                        selectedTarget = found.get(0).value;
                    } else if (found.isEmpty()) {
                        toast("同一局域网暂无可直连设备");
                    }
                    renderPeerSection();
                });
            } catch (Exception e) {
                ui.post(() -> {
                    lanSearching = false;
                    renderPeerSection();
                    toast("局域网发现失败：" + e.getMessage());
                });
            }
        }).start();
    }

    private void renderPeerSection() {
        if (peerChipRow == null || sendCard == null || unpairedCard == null) {
            return;
        }
        List<PeerEntry> combined = new ArrayList<>();
        combined.addAll(pairedEntries);
        combined.addAll(lanEntries);

        boolean hasAny = !combined.isEmpty() || !selectedTarget.isEmpty();
        peerScroll.setVisibility(hasAny ? View.VISIBLE : View.GONE);
        sendCard.setVisibility(hasAny ? View.VISIBLE : View.GONE);
        unpairedCard.setVisibility(hasAny ? View.GONE : View.VISIBLE);

        if (selectedTarget.isEmpty() && !combined.isEmpty()) {
            selectedTarget = combined.get(0).value;
        }
        if (sendTargetTitle != null) {
            sendTargetTitle.setText("发往 " + resolveTargetName(selectedTarget));
        }

        peerChipRow.removeAllViews();
        for (int i = 0; i < combined.size(); i++) {
            final PeerEntry entry = combined.get(i);
            final boolean sel = entry.value.equals(selectedTarget);

            TextView chip = label(entry.name, 13, sel ? cOnPrimary : cOnSurface, true);
            chip.setPadding(dp(14), dp(8), dp(14), dp(8));
            chip.setSingleLine(true);
            chip.setBackground(ripple(roundBox(
                    sel ? cPrimaryPressed : cCard,
                    sel ? cPrimaryPressed : cBorder, 99), 99));
            chip.setOnClickListener(v -> {
                selectedTarget = entry.value;
                saveLastPeer(selectedTarget);
                renderPeerSection();
            });
            LinearLayout.LayoutParams lp = new LinearLayout.LayoutParams(WRAP, WRAP);
            lp.rightMargin = dp(8);
            peerChipRow.addView(chip, lp);
        }

        TextView addBtn = label("+", 17, cOnSurface, true);
        addBtn.setGravity(Gravity.CENTER);
        addBtn.setBackground(ripple(roundBox(cCard, cBorder, 99), 99));
        addBtn.setContentDescription("添加设备");
        addBtn.setOnClickListener(v -> openPairSheet(0));
        LinearLayout.LayoutParams addLp = new LinearLayout.LayoutParams(dp(34), dp(34));
        addLp.rightMargin = dp(8);
        peerChipRow.addView(addBtn, addLp);

        TextView lanBtn = label(lanSearching ? "搜索中…" : "局域网", 12, cOnSurfaceVariant, false);
        lanBtn.setPadding(dp(11), dp(7), dp(11), dp(7));
        lanBtn.setBackground(ripple(roundBox(cCard, cBorder, 99), 99));
        lanBtn.setOnClickListener(v -> discover());
        peerChipRow.addView(lanBtn, new LinearLayout.LayoutParams(WRAP, WRAP));
    }

    private String resolveTargetName(String target) {
        if (target == null || target.isEmpty()) {
            return "未选择设备";
        }
        for (PeerEntry e : pairedEntries) {
            if (e.value.equals(target)) {
                return e.name;
            }
        }
        for (PeerEntry e : lanEntries) {
            if (e.value.equals(target)) {
                return e.name;
            }
        }
        return target.length() > 12 ? "设备 " + target.substring(0, 12) : target;
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
            final String text = buf.toString("UTF-8").trim();
            if (text.isEmpty()) {
                toast("配对文件为空");
                return;
            }
            new AlertDialog.Builder(this)
                    .setTitle("信任这台设备？")
                    .setMessage("设备 ID：" + shortPeerId(text) + "\n\n导入后该设备可向你发送文件。")
                    .setNegativeButton("取消", null)
                    .setPositiveButton("导入并信任", (d, w) -> {
                        try {
                            Mobile.pairImport(text, pairedFile().getAbsolutePath());
                            toast("已导入配对设备");
                            loadPairedPeers();
                        } catch (Exception e) {
                            toast("导入失败：" + e.getMessage());
                        }
                    })
                    .show();
        } catch (Exception e) {
            toast("读取配对文件失败：" + e.getMessage());
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
            if (!id.isEmpty()) {
                selectedTarget = id;
            }
        } catch (Exception e) {
            Log.w(TAG, "applyDebugPeerId", e);
        }
    }

    private void prefillLastPeer() {
        String last = getPreferences(MODE_PRIVATE).getString("lastPeer", "");
        if (!last.isEmpty() && selectedTarget.isEmpty()) {
            selectedTarget = last;
        }
    }

    private void saveLastPeer(String peer) {
        getPreferences(MODE_PRIVATE).edit().putString("lastPeer", peer).apply();
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

    // ================= Models & Custom Views =================

    private static class PeerEntry {
        final String name;
        final String value;

        PeerEntry(String name, String value) {
            this.name = name;
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
        final String dir;  // "recv" | "send"
        final String peer;

        ReceivedEntry(String name, String uri, String mime, String via, long bytes, long ts,
                      String dir, String peer) {
            this.name = name;
            this.uri = uri;
            this.mime = mime;
            this.via = via;
            this.bytes = bytes;
            this.ts = ts;
            this.dir = (dir == null || dir.isEmpty()) ? "recv" : dir;
            this.peer = peer == null ? "" : peer;
        }
    }

    private class FerryMarkDrawable extends Drawable {
        private final Paint p = new Paint(Paint.ANTI_ALIAS_FLAG);
        private final Path boat = new Path();
        private final Path sail = new Path();
        private final Path wave = new Path();

        @Override
        public void draw(Canvas canvas) {
            android.graphics.Rect b = getBounds();
            float w = b.width();
            float h = b.height();
            p.setStyle(Paint.Style.FILL);
            p.setColor(0xFF0D9488);
            canvas.drawRoundRect(0, 0, w, h, w * 0.22f, h * 0.22f, p);

            p.setColor(Color.WHITE);
            boat.reset();
            boat.moveTo(w * 0.20f, h * 0.48f);
            boat.lineTo(w * 0.80f, h * 0.37f);
            boat.lineTo(w * 0.67f, h * 0.62f);
            boat.lineTo(w * 0.30f, h * 0.62f);
            boat.close();
            canvas.drawPath(boat, p);

            sail.reset();
            sail.moveTo(w * 0.37f, h * 0.48f);
            sail.lineTo(w * 0.50f, h * 0.26f);
            sail.lineTo(w * 0.61f, h * 0.45f);
            sail.close();
            canvas.drawPath(sail, p);

            p.setColor(0xFFCCFBF1);
            wave.reset();
            wave.moveTo(w * 0.22f, h * 0.70f);
            wave.quadTo(w * 0.50f, h * 0.65f, w * 0.78f, h * 0.69f);
            wave.quadTo(w * 0.52f, h * 0.76f, w * 0.22f, h * 0.70f);
            wave.close();
            canvas.drawPath(wave, p);
        }

        @Override public void setAlpha(int alpha) {}
        @Override public void setColorFilter(ColorFilter cf) {}
        @Override public int getOpacity() { return PixelFormat.TRANSLUCENT; }
    }

    private class IconDrawable extends Drawable {
        static final int SCAN = 0;
        static final int SETTINGS = 1;
        static final int BACK = 2;
        static final int UPLOAD = 3;
        static final int CLIP = 4;

        private final int kind;
        private final Paint paint = new Paint(Paint.ANTI_ALIAS_FLAG);

        IconDrawable(int kind, int color) {
            this.kind = kind;
            paint.setColor(color);
            paint.setStyle(Paint.Style.STROKE);
            paint.setStrokeWidth(dp(1.8f));
            paint.setStrokeCap(Paint.Cap.ROUND);
            paint.setStrokeJoin(Paint.Join.ROUND);
        }

        @Override
        public void draw(Canvas c) {
            android.graphics.Rect b = getBounds();
            float w = b.width();
            float h = b.height();
            Path p = new Path();
            if (kind == BACK) {
                p.moveTo(w * 0.62f, h * 0.25f);
                p.lineTo(w * 0.36f, h * 0.50f);
                p.lineTo(w * 0.62f, h * 0.75f);
                c.drawPath(p, paint);
            } else if (kind == SCAN) {
                float d = w * 0.16f;
                float l = w * 0.16f, t = h * 0.16f, r = w * 0.84f, bt = h * 0.84f;
                p.moveTo(l, t + d); p.lineTo(l, t); p.lineTo(l + d, t);
                p.moveTo(r - d, t); p.lineTo(r, t); p.lineTo(r, t + d);
                p.moveTo(r, bt - d); p.lineTo(r, bt); p.lineTo(r - d, bt);
                p.moveTo(l + d, bt); p.lineTo(l, bt); p.lineTo(l, bt - d);
                p.moveTo(w * 0.25f, h * 0.50f); p.lineTo(w * 0.75f, h * 0.50f);
                c.drawPath(p, paint);
            } else if (kind == SETTINGS) {
                c.drawCircle(w * 0.5f, h * 0.5f, w * 0.14f, paint);
                for (int i = 0; i < 8; i++) {
                    double a = i * Math.PI / 4.0;
                    float x1 = w * 0.5f + (float) Math.cos(a) * w * 0.25f;
                    float y1 = h * 0.5f + (float) Math.sin(a) * h * 0.25f;
                    float x2 = w * 0.5f + (float) Math.cos(a) * w * 0.34f;
                    float y2 = h * 0.5f + (float) Math.sin(a) * h * 0.34f;
                    c.drawLine(x1, y1, x2, y2, paint);
                }
            } else if (kind == UPLOAD) {
                p.moveTo(w * 0.5f, h * 0.62f); p.lineTo(w * 0.5f, h * 0.18f);
                p.moveTo(w * 0.30f, h * 0.36f); p.lineTo(w * 0.5f, h * 0.17f); p.lineTo(w * 0.70f, h * 0.36f);
                p.moveTo(w * 0.20f, h * 0.65f); p.lineTo(w * 0.20f, h * 0.80f);
                p.lineTo(w * 0.80f, h * 0.80f); p.lineTo(w * 0.80f, h * 0.65f);
                c.drawPath(p, paint);
            } else { // CLIP
                c.drawRoundRect(new RectF(w * 0.34f, h * 0.34f, w * 0.82f, h * 0.82f),
                        w * 0.08f, h * 0.08f, paint);
                p.moveTo(w * 0.28f, h * 0.62f); p.lineTo(w * 0.20f, h * 0.62f);
                p.lineTo(w * 0.20f, h * 0.20f); p.lineTo(w * 0.62f, h * 0.20f);
                p.lineTo(w * 0.62f, h * 0.28f);
                c.drawPath(p, paint);
            }
        }

        @Override public void setAlpha(int alpha) {}
        @Override public void setColorFilter(ColorFilter cf) {}
        @Override public int getOpacity() { return PixelFormat.TRANSLUCENT; }
    }

    private class CornerBracketsView extends View {
        private final Paint p = new Paint(Paint.ANTI_ALIAS_FLAG);

        CornerBracketsView() {
            super(MainActivity.this);
            p.setColor(Color.parseColor("#14B8A6"));
            p.setStyle(Paint.Style.STROKE);
            p.setStrokeWidth(dp(2.5f));
            p.setStrokeCap(Paint.Cap.ROUND);
        }

        @Override
        protected void onDraw(Canvas canvas) {
            float w = getWidth();
            float h = getHeight();
            float arm = dp(20);
            float pad = dp(2);
            Path path = new Path();
            path.moveTo(pad, pad + arm); path.lineTo(pad, pad); path.lineTo(pad + arm, pad);
            path.moveTo(w - pad - arm, pad); path.lineTo(w - pad, pad); path.lineTo(w - pad, pad + arm);
            path.moveTo(w - pad, h - pad - arm); path.lineTo(w - pad, h - pad); path.lineTo(w - pad - arm, h - pad);
            path.moveTo(pad + arm, h - pad); path.lineTo(pad, h - pad); path.lineTo(pad, h - pad - arm);
            canvas.drawPath(path, p);
        }
    }

    // ================= Widget & Style Helpers =================

    private TextView label(String text, float sp, int color, boolean medium) {
        TextView t = new TextView(this);
        t.setText(text);
        t.setTextSize(sp);
        t.setTextColor(color);
        t.setIncludeFontPadding(false);
        t.setTypeface(medium ? Typeface.create("sans-serif-medium", Typeface.NORMAL) : Typeface.DEFAULT);
        return t;
    }

    private EditText modalInput(String hint) {
        EditText e = new EditText(this);
        e.setHint(hint);
        e.setHintTextColor(cOnSurfaceVariant);
        e.setTextColor(cOnSurface);
        e.setTextSize(14);
        e.setSingleLine(true);
        e.setGravity(Gravity.CENTER_VERTICAL);
        e.setPadding(dp(12), 0, dp(12), 0);
        e.setBackground(roundBox(cSurface, cBorder, 10));
        return e;
    }

    private Button baseBtn(String text, float sp) {
        Button b = new Button(this);
        b.setText(text);
        b.setTextSize(sp);
        b.setAllCaps(false);
        b.setIncludeFontPadding(false);
        b.setTypeface(Typeface.create("sans-serif-medium", Typeface.NORMAL));
        b.setGravity(Gravity.CENTER);
        b.setPadding(dp(14), 0, dp(14), 0);
        b.setMinHeight(0);
        b.setMinimumHeight(0);
        b.setMinWidth(0);
        b.setMinimumWidth(0);
        b.setElevation(0);
        b.setStateListAnimator(null);
        return b;
    }

    private Button filledBtn(String text, int bg, int fg) {
        Button b = baseBtn(text, 13);
        b.setBackground(ripple(roundBox(bg, 0, 12), 12));
        b.setTextColor(fg);
        return b;
    }

    private Button smallFilledBtn(String text, int bg, int fg, View.OnClickListener click) {
        Button b = baseBtn(text, 12);
        b.setPadding(dp(12), 0, dp(12), 0);
        b.setBackground(ripple(roundBox(bg, 0, 10), 10));
        b.setTextColor(fg);
        b.setOnClickListener(click);
        return b;
    }

    private Button smallOutlineBtn(String text, View.OnClickListener click) {
        Button b = baseBtn(text, 12);
        b.setPadding(dp(11), 0, dp(11), 0);
        b.setBackground(ripple(roundBox(cSurface, cBorder, 10), 10));
        b.setTextColor(cOnSurface);
        b.setOnClickListener(click);
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

    private void stylePathBadge(TextView t, String via) {
        t.setText(pathLabel(via));
        if ("lan".equalsIgnoreCase(via)) {
            t.setTextColor(cOnPrimaryContainer);
            t.setBackground(roundBox(cPrimaryContainer, 0, 99));
        } else if ("punch".equalsIgnoreCase(via)) {
            t.setTextColor(cWarn);
            t.setBackground(roundBox(cWarnContainer, 0, 99));
        } else {
            t.setTextColor(cOnSurface);
            t.setBackground(roundBox(cBorder, 0, 99));
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

    // ================= Platform & Utility Helpers =================

    private void startDotPulse() {
        if (dotAnimator != null) {
            dotAnimator.cancel();
        }
        dotAnimator = ValueAnimator.ofFloat(1f, 0.4f);
        dotAnimator.setDuration(900);
        dotAnimator.setRepeatMode(ValueAnimator.REVERSE);
        dotAnimator.setRepeatCount(ValueAnimator.INFINITE);
        dotAnimator.setInterpolator(new AccelerateDecelerateInterpolator());
        dotAnimator.addUpdateListener(a -> {
            if (recvPulseDot != null) {
                recvPulseDot.setAlpha((Float) a.getAnimatedValue());
            }
        });
        dotAnimator.start();
    }

    private void stopDotPulse() {
        if (dotAnimator != null) {
            dotAnimator.cancel();
            dotAnimator = null;
        }
        if (recvPulseDot != null) {
            recvPulseDot.setAlpha(1f);
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
        return peer != null && HEX64.matcher(peer).matches();
    }

    private static boolean isLanTarget(String peer) {
        if (peer == null) {
            return false;
        }
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
