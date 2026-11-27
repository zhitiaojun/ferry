package cn.cwlindean.ferry;

import android.Manifest;
import android.animation.ValueAnimator;
import android.app.Activity;
import android.app.ActivityManager;
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
import android.graphics.Rect;
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
import android.provider.DocumentsContract;
import android.provider.MediaStore;
import android.provider.OpenableColumns;
import android.provider.Settings;
import android.text.InputFilter;
import android.text.InputType;
import android.text.TextUtils;
import android.util.Log;
import android.util.Range;
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
import android.widget.ProgressBar;
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
import java.nio.charset.StandardCharsets;
import java.text.SimpleDateFormat;
import java.util.ArrayList;
import java.util.Arrays;
import java.util.Collections;
import java.util.Date;
import java.util.HashMap;
import java.util.HashSet;
import java.util.Iterator;
import java.util.List;
import java.util.Locale;
import java.util.Map;
import java.util.Set;
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
    private static final int PICK_FILES = 42;
    private static final int PICK_PAIR_FILE = 43;
    private static final int REQ_CAMERA = 44;
    private static final int PICK_DOWNLOAD_DIR = 45;
    private static final int PICK_TEMP_SEND = 46;
    private static final int PICK_TEMP_UPLOAD = 47;
    private static final int PICK_FOLDER = 48;
    private static final int PAIR_TTL_SEC = 600;
    private static final int MATCH = ViewGroup.LayoutParams.MATCH_PARENT;
    private static final int WRAP = ViewGroup.LayoutParams.WRAP_CONTENT;
    private static final Pattern HEX64 = Pattern.compile("^[0-9a-fA-F]{64}$");
    private static final Pattern TEMP_SHARE_URL = Pattern.compile("^https?://[^/]+/s/[A-Za-z0-9_-]+(?:[?#].*)?$");

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
    private String customDownloadTreeUri = "";
    private String customDownloadLabel = "";
    private String webShareUrl = "";
    private boolean recvRunning;
    private boolean webRunning;
    private Dialog tempShareDialog;
    private Dialog tempJoinDialog;
    private String pendingTempJoinUrl = "";
    private File tempSendStageDir;
    private TextView tempTtlTv;
    private TextView tempUploadTv;
    private LinearLayout tempTxBox;
    private TextView tempTxTitleTv;
    private ProgressBar tempTxBar;
    private TextView tempTxMetaTv;
    private LinearLayout tempJoinTxBox;
    private TextView tempJoinTxTitleTv;
    private ProgressBar tempJoinTxBar;
    private TextView tempJoinTxMetaTv;
    private LinearLayout tempJoinActionRow;
    private Button tempJoinPauseBtn;
    private Button tempJoinCancelBtn;
    private Button tempJoinActionBtn;
    private boolean lanSearching;
    private String signalState = "offline";
    private final Set<String> cloudOnlineSet = new HashSet<>();
    private final Set<String> lanOnlineSet = new HashSet<>();
    private int activePage; // 0 = 传输, 1 = 记录, 2 = 设备, 3 = 设置
    private String lanPort = "";
    private String selectedTarget = "";
    private boolean userPickedTarget;
    private boolean unreadHistoryDot;
    private String activeFilter = "all"; // "all" | "recv" | "send"
    private List<Uri> sharedUris;
    private final List<PeerEntry> pairedEntries = new ArrayList<>();
    private final List<PeerEntry> lanEntries = new ArrayList<>();
    private final List<ReceivedEntry> recentEntries = new ArrayList<>();

    // Top Bar & Floating Capsule Bottom Nav views
    private View backBtn;
    private View brandMarkView;
    private TextView topTitleView;
    private View topActionsWrap;
    private LinearLayout bottomNavWrap;
    private View historyBadgeDot;
    private final LinearLayout[] navTabs = new LinearLayout[4];
    private final ImageView[] navIcons = new ImageView[4];
    private final TextView[] navLabels = new TextView[4];

    // Pages
    private ScrollView pageHome;
    private ScrollView pageHistory;
    private ScrollView pageDevices;
    private ScrollView pageSettings;

    // Live transfer card & progress polling state
    private LinearLayout activeTransferCard;
    private TextView txHeadTv;
    private TextView txChanBadge;
    private TextView txSubTv;
    private ProgressBar txProgressBar;
    private TextView txMetricsTv;
    private LinearLayout txActionRow;
    private Button txPauseBtn;
    private Button txCancelBtn;
    private boolean progressPollingActive;
    private boolean sendBusyFlag;

    // Devices page views
    private LinearLayout savedDevicesList;
    private TextView savedDevicesEmpty;
    private LinearLayout lanDevicesList;
    private TextView lanDevicesEmpty;
    private Button devicesLanBtn;

    // Home views
    private LinearLayout homeSignalBar;
    private View homeSigDot;
    private TextView homeSigText;
    private TextView homeSigHost;
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
    private LinearLayout sendFolderTile;
    private LinearLayout sendTextTile;
    private LinearLayout sendClipTile;
    private LinearLayout homeClipBar;
    private TextView homeClipText;
    private LinearLayout homeLatestCard;
    private TextView homeLatestTitle;
    private TextView homeLatestSub;
    private Button homeLatestBtn;
    private LinearLayout sendResultCard;
    private TextView sendResultBadge;
    private TextView sendResultText;
    private AlertDialog incomingConfirmDialog;
    private String incomingConfirmId;

    // Recent list views (dedicated History tab)
    private final TextView[] filterTabs = new TextView[3];
    private LinearLayout recentListView;
    private LinearLayout recentEmptyView;
    private TextView recentEmptySub;

    // Settings views
    private Button setRecvBtn;
    private TextView setKeepAliveNotifSub;
    private Button setKeepAliveNotifBtn;
    private Button setHideRecentsBtn;
    private TextView setDownloadDirSub;
    private Button setDownloadResetBtn;
    private TextView setServerSub;
    private Button setServerResetBtn;
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

    // External Share Sheet state & views
    private Dialog shareDialog;
    private LinearLayout sharePeersBox;
    private Button shareSendBtn;
    private String shareSelectedTarget = "";
    private List<Uri> shareDialogUris;

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

    private final TransferService.UiBridge uiBridge = new TransferService.UiBridge() {
        @Override
        public void onServiceStateChanged(boolean running, String port, String error) {
            ui.post(() -> {
                if (setRecvBtn != null) {
                    setRecvBtn.setEnabled(true);
                }
                recvRunning = running;
                lanPort = port != null ? port : "";
                if (running) {
                    acquireMulticastLock();
                    pollConnectionStatusOnce();
                } else {
                    releaseMulticastLock();
                    signalState = "offline";
                    cloudOnlineSet.clear();
                    lanOnlineSet.clear();
                }
                if (error != null && !error.isEmpty() && !running) {
                    toast("开启接收失败：" + error);
                }
                refreshReceiveUi();
                refreshSettingsInfo();
            });
        }

        @Override
        public void onConnectionSnapshot(String connJson) {
            ui.post(() -> applyConnectionStatusJson(connJson));
        }

        @Override
        public void onTransferProgressSnapshot(String progressJson) {
            ui.post(() -> applyActiveTransferJson(progressJson));
        }

        @Override
        public void onFilesReceived(List<JSONObject> entries, boolean refreshPeers) {
            ui.post(() -> {
                String firstOkName = "";
                JSONObject firstOutcome = null;
                for (int i = entries.size() - 1; i >= 0; i--) {
                    JSONObject o = entries.get(i);
                    ReceivedEntry re = new ReceivedEntry(
                            o.optString("name", ""),
                            o.optString("uri", ""),
                            o.optString("mime", "application/octet-stream"),
                            o.optString("via", ""),
                            o.optLong("bytes", 0),
                            o.optLong("ts", System.currentTimeMillis()),
                            o.optString("dir", "recv"),
                            o.optString("peer", ""),
                            o.optString("status", ""),
                            o.optString("error", ""));
                    recentEntries.add(0, re);
                    if (re.status.isEmpty() || "ok".equals(re.status)) {
                        if (firstOkName.isEmpty() && !re.name.isEmpty()) {
                            firstOkName = re.name;
                        }
                    } else if (firstOutcome == null) {
                        firstOutcome = o;
                    }
                }
                while (recentEntries.size() > 30) {
                    recentEntries.remove(recentEntries.size() - 1);
                }
                if (activePage != 1 && !entries.isEmpty()) {
                    unreadHistoryDot = true;
                    updateHistoryBadgeDot();
                }
                renderRecentList();
                if (!firstOkName.isEmpty()) {
                    toast("已保存到「" + currentDownloadShortLabel() + "」：" + firstOkName);
                } else if (firstOutcome != null) {
                    String st = firstOutcome.optString("status", "");
                    String nm = firstOutcome.optString("name", "接收文件");
                    String errMsg = firstOutcome.optString("error", "");
                    if ("peer_cancelled".equals(st)) {
                        showSendBanner("peer_cancelled", "对方已取消发送 · " + nm, false);
                        toast("对方已取消传输");
                    } else if ("cancelled".equals(st)) {
                        showSendBanner("cancelled", "已取消接收 · " + nm, false);
                        toast("已取消接收");
                    } else {
                        showSendBanner("fail", "接收失败：" + (errMsg.isEmpty() ? nm : errMsg), true);
                    }
                }
                if (refreshPeers) {
                    loadPairedPeers();
                }
            });
        }

        @Override
        public void onConfirmIncoming(String reqJson) {
            ui.post(() -> showIncomingConfirmDialog(reqJson));
        }
    };

    private final Runnable progressPollTask = new Runnable() {
        @Override
        public void run() {
            if (!progressPollingActive) {
                return;
            }
            pollActiveTransferOnce();
            ui.postDelayed(this, 200);
        }
    };

    @Override
    protected void onCreate(Bundle savedInstanceState) {
        super.onCreate(savedInstanceState);
        loadThemeTokens();
        applyWindowChrome();
        migrateLegacyState();
        deviceName = loadSavedName();
        loadDownloadDirPrefs();
        TransferService.ensureChannels(this);
        setContentView(buildUi());
        applyExcludeFromRecents(isHideRecentsEnabled());

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
        discoverSilent();
    }

    @Override
    protected void onStart() {
        super.onStart();
        TransferService.setUiBridge(uiBridge);
        recvRunning = TransferService.isRecvRunning();
        lanPort = TransferService.getLanPort();
        if (recvRunning) {
            acquireMulticastLock();
        }
        refreshReceiveUi();
        refreshSettingsInfo();
        loadRecentState();
        pollConnectionStatusOnce();
        pollActiveTransferOnce();
    }

    @Override
    protected void onResume() {
        super.onResume();
        TransferService.setUiBridge(uiBridge);
        recvRunning = TransferService.isRecvRunning();
        lanPort = TransferService.getLanPort();
        loadRecentState();
        pollActiveTransferOnce();
        if (recvRunning) {
            ioPool.execute(() -> {
                try {
                    Mobile.updateNetworkHint(lanHint());
                    Mobile.reconnectAutoReceive();
                } catch (Exception ignored) {
                }
            });
            pollConnectionStatusOnce();
        }
        if (pairDialog != null && pairDialog.isShowing() && pairMode == 0) {
            startCameraIfReady();
        }
        applyExcludeFromRecents(isHideRecentsEnabled());
        refreshKeepAliveNotifUi();
        refreshHideRecentsUi();
        refreshClipboardPreviewBar();
    }

    @Override
    public void onWindowFocusChanged(boolean hasFocus) {
        super.onWindowFocusChanged(hasFocus);
        if (hasFocus) {
            refreshClipboardPreviewBar();
        }
    }

    @Override
    protected void onPause() {
        stopCamera();
        super.onPause();
    }

    @Override
    protected void onStop() {
        TransferService.clearUiBridge(uiBridge);
        dismissIncomingConfirmDialog(false);
        stopProgressPolling();
        releaseMulticastLock();
        super.onStop();
    }

    private void showIncomingConfirmDialog(String reqJson) {
        String reqId = "";
        String peer = "局域网未知设备";
        String addr = "";
        int fileCount = 0;
        long totalBytes = 0;
        JSONArray files = null;
        try {
            JSONObject o = new JSONObject(reqJson);
            reqId = o.optString("id", "");
            String p = o.optString("peer", "").trim();
            if (!p.isEmpty()) {
                peer = p;
            }
            addr = o.optString("addr", "");
            fileCount = o.optInt("file_count", 0);
            totalBytes = o.optLong("total_bytes", 0);
            files = o.optJSONArray("files");
        } catch (Exception ignored) {
        }
        if (reqId.isEmpty()) {
            return;
        }
        if (isFinishing() || isDestroyed()) {
            Mobile.respondIncoming(reqId, false);
            return;
        }
        dismissIncomingConfirmDialog(false);
        incomingConfirmId = reqId;

        StringBuilder body = new StringBuilder();
        body.append("来自：").append(peer);
        if (!addr.isEmpty()) {
            body.append(" (").append(addr).append(")");
        }
        body.append("\n共 ").append(fileCount).append(" 个文件（").append(fmtBytes(totalBytes)).append("）");
        if (files != null && files.length() > 0) {
            int limit = Math.min(files.length(), 3);
            for (int i = 0; i < limit; i++) {
                body.append("\n• ").append(files.optString(i, ""));
            }
            if (fileCount > limit) {
                body.append("\n• 等共 ").append(fileCount).append(" 个文件");
            }
        }
        body.append("\n\n该设备未配对，仅本次临时接收");

        final String curId = reqId;
        AlertDialog dlg = new AlertDialog.Builder(this)
                .setTitle("收到局域网临时文件")
                .setMessage(body.toString())
                .setPositiveButton("接收", (d, w) -> {
                    if (curId.equals(incomingConfirmId)) {
                        incomingConfirmId = null;
                    }
                    incomingConfirmDialog = null;
                    Mobile.respondIncoming(curId, true);
                })
                .setNegativeButton("拒绝", (d, w) -> {
                    if (curId.equals(incomingConfirmId)) {
                        incomingConfirmId = null;
                    }
                    incomingConfirmDialog = null;
                    Mobile.respondIncoming(curId, false);
                })
                .setOnCancelListener(d -> {
                    if (curId.equals(incomingConfirmId)) {
                        incomingConfirmId = null;
                    }
                    incomingConfirmDialog = null;
                    Mobile.respondIncoming(curId, false);
                })
                .create();
        incomingConfirmDialog = dlg;
        dlg.show();
    }

    private void dismissIncomingConfirmDialog(boolean accept) {
        if (incomingConfirmDialog != null) {
            try {
                incomingConfirmDialog.dismiss();
            } catch (Exception ignored) {
            }
            incomingConfirmDialog = null;
        }
        if (incomingConfirmId != null && !incomingConfirmId.isEmpty()) {
            String id = incomingConfirmId;
            incomingConfirmId = null;
            Mobile.respondIncoming(id, accept);
        }
    }

    @Override
    protected void onNewIntent(Intent intent) {
        super.onNewIntent(intent);
        setIntent(intent);
        applyExcludeFromRecents(isHideRecentsEnabled());
        handleShareIntent(intent);
    }

    @Override
    public void onBackPressed() {
        if (shareDialog != null && shareDialog.isShowing()) {
            dismissShareSheet(true);
            return;
        }
        if (pairDialog != null && pairDialog.isShowing()) {
            closePairSheet();
            return;
        }
        if (activePage != 0) {
            showPage(0);
            return;
        }
        super.onBackPressed();
    }

    @Override
    protected void onDestroy() {
        TransferService.clearUiBridge(uiBridge);
        dismissShareSheet(true);
        closePairSheet();
        stopProgressPolling();
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

        scaffold.addView(buildTopBar(), new LinearLayout.LayoutParams(MATCH, dp(52)));

        FrameLayout contentHost = new FrameLayout(this);
        pageHome = buildHomePage();
        pageHistory = buildHistoryPage();
        pageHistory.setVisibility(View.GONE);
        pageDevices = buildDevicesPage();
        pageDevices.setVisibility(View.GONE);
        pageSettings = buildSettingsPage();
        pageSettings.setVisibility(View.GONE);
        contentHost.addView(pageHome, new FrameLayout.LayoutParams(MATCH, MATCH));
        contentHost.addView(pageHistory, new FrameLayout.LayoutParams(MATCH, MATCH));
        contentHost.addView(pageDevices, new FrameLayout.LayoutParams(MATCH, MATCH));
        contentHost.addView(pageSettings, new FrameLayout.LayoutParams(MATCH, MATCH));

        scaffold.addView(contentHost, new LinearLayout.LayoutParams(MATCH, 0, 1f));
        bottomNavWrap = buildBottomNav();
        scaffold.addView(bottomNavWrap, new LinearLayout.LayoutParams(MATCH, WRAP));
        showPage(0);
        return scaffold;
    }

    private LinearLayout buildTopBar() {
        LinearLayout bar = new LinearLayout(this);
        bar.setOrientation(LinearLayout.HORIZONTAL);
        bar.setGravity(Gravity.CENTER_VERTICAL);
        bar.setPadding(dp(20), dp(4), dp(14), dp(4));

        // Left: back button (unused on top-level tabs) + brand mark + title
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

        // Right: quick QR scan action
        LinearLayout actions = new LinearLayout(this);
        actions.setOrientation(LinearLayout.HORIZONTAL);
        actions.setGravity(Gravity.CENTER_VERTICAL);
        topActionsWrap = actions;

        ImageView scanBtn = iconActionBtn(IconDrawable.SCAN, "扫一扫", v -> openPairSheet(0));
        actions.addView(scanBtn, new LinearLayout.LayoutParams(dp(40), dp(40)));

        bar.addView(actions, new LinearLayout.LayoutParams(WRAP, WRAP));
        return bar;
    }

    private LinearLayout buildBottomNav() {
        LinearLayout outer = new LinearLayout(this);
        outer.setOrientation(LinearLayout.VERTICAL);
        outer.setBackgroundColor(cSurface);
        outer.setPadding(dp(14), dp(6), dp(14), dp(10));

        LinearLayout capsule = new LinearLayout(this);
        capsule.setOrientation(LinearLayout.HORIZONTAL);
        capsule.setGravity(Gravity.CENTER_VERTICAL);
        capsule.setBackground(roundBox(cCard, cBorder, 99));
        capsule.setPadding(dp(5), dp(5), dp(5), dp(5));
        if (Build.VERSION.SDK_INT >= 21) {
            capsule.setElevation(dp(4));
        }

        int[] icons = {IconDrawable.TRANSFER, IconDrawable.HISTORY, IconDrawable.DEVICES, IconDrawable.SETTINGS};
        String[] titles = {"传输", "记录", "设备", "设置"};
        for (int i = 0; i < 4; i++) {
            final int idx = i;
            LinearLayout tab = new LinearLayout(this);
            tab.setOrientation(LinearLayout.HORIZONTAL);
            tab.setGravity(Gravity.CENTER);
            tab.setPadding(dp(6), 0, dp(6), 0);
            tab.setOnClickListener(v -> showPage(idx));

            FrameLayout iconWrap = new FrameLayout(this);
            ImageView iv = new ImageView(this);
            iv.setImageDrawable(new IconDrawable(icons[i], cOnSurfaceVariant));
            iconWrap.addView(iv, new FrameLayout.LayoutParams(dp(16), dp(16), Gravity.CENTER));

            if (i == 1) {
                historyBadgeDot = new View(this);
                historyBadgeDot.setBackground(oval(Color.parseColor("#10B981")));
                historyBadgeDot.setVisibility(View.GONE);
                FrameLayout.LayoutParams bLp = new FrameLayout.LayoutParams(dp(6), dp(6), Gravity.TOP | Gravity.END);
                iconWrap.addView(historyBadgeDot, bLp);
            }

            LinearLayout.LayoutParams iwLp = new LinearLayout.LayoutParams(dp(20), dp(20));
            iwLp.rightMargin = dp(4);
            tab.addView(iconWrap, iwLp);

            TextView tv = label(titles[i], 12, cOnSurfaceVariant, true);
            tv.setSingleLine(true);
            tab.addView(tv, new LinearLayout.LayoutParams(WRAP, WRAP));

            navTabs[i] = tab;
            navIcons[i] = iv;
            navLabels[i] = tv;
            LinearLayout.LayoutParams tabLp = new LinearLayout.LayoutParams(0, dp(42), 1f);
            if (i < 3) {
                tabLp.rightMargin = dp(4);
            }
            capsule.addView(tab, tabLp);
        }
        outer.addView(capsule, new LinearLayout.LayoutParams(MATCH, dp(52)));
        return outer;
    }

    private void updateHistoryBadgeDot() {
        if (historyBadgeDot != null) {
            historyBadgeDot.setVisibility(unreadHistoryDot && activePage != 1 ? View.VISIBLE : View.GONE);
        }
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
        pageHome.setVisibility(page == 0 ? View.VISIBLE : View.GONE);
        pageHistory.setVisibility(page == 1 ? View.VISIBLE : View.GONE);
        pageDevices.setVisibility(page == 2 ? View.VISIBLE : View.GONE);
        pageSettings.setVisibility(page == 3 ? View.VISIBLE : View.GONE);
        backBtn.setVisibility(View.GONE);
        brandMarkView.setVisibility(View.VISIBLE);
        topActionsWrap.setVisibility(page == 3 ? View.GONE : View.VISIBLE);
        String[] pageTitles = {"轻渡", "传输记录", "设备", "设置"};
        topTitleView.setText(page >= 0 && page < pageTitles.length ? pageTitles[page] : "轻渡");

        if (page == 1) {
            unreadHistoryDot = false;
        }
        updateHistoryBadgeDot();

        int[] icons = {IconDrawable.TRANSFER, IconDrawable.HISTORY, IconDrawable.DEVICES, IconDrawable.SETTINGS};
        for (int i = 0; i < 4; i++) {
            boolean sel = (i == page);
            if (navIcons[i] != null) {
                navIcons[i].setImageDrawable(new IconDrawable(icons[i], sel ? cOnPrimaryContainer : cOnSurfaceVariant));
            }
            if (navLabels[i] != null) {
                navLabels[i].setTextColor(sel ? cOnPrimaryContainer : cOnSurfaceVariant);
            }
            if (navTabs[i] != null) {
                navTabs[i].setBackground(ripple(roundBox(
                        sel ? cPrimaryContainer : Color.TRANSPARENT, 0, 99), 99));
            }
        }

        if (page == 1) {
            renderRecentList();
        } else if (page == 2) {
            renderDevicesPage();
        } else if (page == 3) {
            refreshSettingsInfo();
        } else {
            refreshClipboardPreviewBar();
        }
    }

    // ================= Page 0: Home (Send Launchpad) =================

    private ScrollView buildHomePage() {
        ScrollView sv = new ScrollView(this);
        sv.setFillViewport(true);

        LinearLayout root = new LinearLayout(this);
        root.setOrientation(LinearLayout.VERTICAL);
        root.setPadding(dp(18), dp(10), dp(18), dp(14));

        // 0. Progressive-disclosure Signaling Status Bar (HIDDEN when connected normally)
        homeSignalBar = new LinearLayout(this);
        homeSignalBar.setOrientation(LinearLayout.HORIZONTAL);
        homeSignalBar.setGravity(Gravity.CENTER_VERTICAL);
        homeSignalBar.setPadding(dp(12), dp(9), dp(12), dp(9));
        homeSignalBar.setBackground(ripple(roundBox(cWarnContainer, cWarn, 12), 12));
        homeSignalBar.setVisibility(View.GONE);
        homeSignalBar.setOnClickListener(v -> {
            if (!recvRunning) {
                getPreferences(MODE_PRIVATE).edit().putBoolean("recvAuto", true).apply();
                startAllReceive();
            } else if (!"online".equals(signalState)) {
                ioPool.execute(() -> {
                    try {
                        Mobile.reconnectAutoReceive();
                    } catch (Exception ignored) {
                    }
                });
                pollConnectionStatusOnce();
                toast("正在重连云端信令…");
            }
        });

        homeSigDot = new View(this);
        homeSigDot.setBackground(oval(cWarn));
        LinearLayout.LayoutParams hsDotLp = new LinearLayout.LayoutParams(dp(8), dp(8));
        hsDotLp.rightMargin = dp(8);
        homeSignalBar.addView(homeSigDot, hsDotLp);

        homeSigText = label("云端未连接 · 同 Wi-Fi 仍可直传", 12, cWarn, true);
        homeSigText.setSingleLine(true);
        homeSigText.setEllipsize(TextUtils.TruncateAt.END);
        homeSignalBar.addView(homeSigText, new LinearLayout.LayoutParams(0, WRAP, 1f));

        homeSigHost = label("重试", 12, cWarn, true);
        LinearLayout.LayoutParams hsHostLp = new LinearLayout.LayoutParams(WRAP, WRAP);
        hsHostLp.leftMargin = dp(8);
        homeSignalBar.addView(homeSigHost, hsHostLp);

        LinearLayout.LayoutParams hsLp = new LinearLayout.LayoutParams(MATCH, WRAP);
        hsLp.bottomMargin = dp(12);
        root.addView(homeSignalBar, hsLp);

        // 1. Horizontal Paired / LAN Device Selector Bar (includes unified + 临时扫码传 chip)
        peerScroll = new HorizontalScrollView(this);
        peerScroll.setHorizontalScrollBarEnabled(false);
        peerChipRow = new LinearLayout(this);
        peerChipRow.setOrientation(LinearLayout.HORIZONTAL);
        peerChipRow.setGravity(Gravity.CENTER_VERTICAL);
        peerScroll.addView(peerChipRow, new FrameLayout.LayoutParams(WRAP, WRAP));
        LinearLayout.LayoutParams psLp = new LinearLayout.LayoutParams(MATCH, WRAP);
        psLp.bottomMargin = dp(12);
        root.addView(peerScroll, psLp);

        // 2a. Live Active Transfer Progress Card (PINNED AT TOP when active)
        activeTransferCard = new LinearLayout(this);
        activeTransferCard.setOrientation(LinearLayout.VERTICAL);
        activeTransferCard.setPadding(dp(16), dp(14), dp(16), dp(14));
        activeTransferCard.setBackground(roundBox(cCard, cPrimary, 16));
        activeTransferCard.setVisibility(View.GONE);

        LinearLayout txTop = new LinearLayout(this);
        txTop.setOrientation(LinearLayout.HORIZONTAL);
        txTop.setGravity(Gravity.CENTER_VERTICAL);

        txHeadTv = label("正在传输", 13, cOnSurface, true);
        txHeadTv.setSingleLine(true);
        txHeadTv.setEllipsize(TextUtils.TruncateAt.END);
        txTop.addView(txHeadTv, new LinearLayout.LayoutParams(0, WRAP, 1f));

        txChanBadge = label("", 11, cOnPrimaryContainer, true);
        txChanBadge.setPadding(dp(8), dp(2), dp(8), dp(2));
        txChanBadge.setVisibility(View.GONE);
        LinearLayout.LayoutParams cbLp = new LinearLayout.LayoutParams(WRAP, WRAP);
        cbLp.leftMargin = dp(8);
        txTop.addView(txChanBadge, cbLp);
        activeTransferCard.addView(txTop, new LinearLayout.LayoutParams(MATCH, WRAP));

        txSubTv = label("连接中…", 12, cOnSurfaceVariant, false);
        txSubTv.setSingleLine(true);
        txSubTv.setEllipsize(TextUtils.TruncateAt.MIDDLE);
        LinearLayout.LayoutParams tsLp = new LinearLayout.LayoutParams(MATCH, WRAP);
        tsLp.topMargin = dp(5);
        activeTransferCard.addView(txSubTv, tsLp);

        txProgressBar = new ProgressBar(this, null, android.R.attr.progressBarStyleHorizontal);
        txProgressBar.setMax(1000);
        txProgressBar.setIndeterminate(true);
        txProgressBar.setProgressTintList(ColorStateList.valueOf(cPrimary));
        txProgressBar.setIndeterminateTintList(ColorStateList.valueOf(cPrimary));
        txProgressBar.setProgressBackgroundTintList(ColorStateList.valueOf(cBorder));
        LinearLayout.LayoutParams pbLp = new LinearLayout.LayoutParams(MATCH, dp(6));
        pbLp.topMargin = dp(10);
        activeTransferCard.addView(txProgressBar, pbLp);

        txMetricsTv = label("准备传输数据…", 12, cOnSurfaceVariant, false);
        txMetricsTv.setSingleLine(false);
        txMetricsTv.setMaxLines(2);
        txMetricsTv.setEllipsize(TextUtils.TruncateAt.END);
        LinearLayout.LayoutParams tmLp = new LinearLayout.LayoutParams(MATCH, WRAP);
        tmLp.topMargin = dp(8);
        activeTransferCard.addView(txMetricsTv, tmLp);

        txActionRow = new LinearLayout(this);
        txActionRow.setOrientation(LinearLayout.HORIZONTAL);
        txActionRow.setVisibility(View.GONE);
        LinearLayout.LayoutParams tarLp = new LinearLayout.LayoutParams(MATCH, WRAP);
        tarLp.topMargin = dp(10);
        txPauseBtn = smallOutlineBtn("暂停", v -> toggleActiveTransferPause());
        LinearLayout.LayoutParams tpLp = new LinearLayout.LayoutParams(0, dp(34), 1f);
        tpLp.rightMargin = dp(8);
        txActionRow.addView(txPauseBtn, tpLp);
        txCancelBtn = smallOutlineBtn("取消传输", v -> cancelActiveTransfer());
        txCancelBtn.setTextColor(cError);
        txActionRow.addView(txCancelBtn, new LinearLayout.LayoutParams(0, dp(34), 1f));
        activeTransferCard.addView(txActionRow, tarLp);

        LinearLayout.LayoutParams txLp = new LinearLayout.LayoutParams(MATCH, WRAP);
        txLp.bottomMargin = dp(14);
        root.addView(activeTransferCard, txLp);

        // 2b. Primary Send Launchpad
        sendCard = new LinearLayout(this);
        sendCard.setOrientation(LinearLayout.VERTICAL);

        shareBannerView = label("", 12, cOnPrimaryContainer, true);
        shareBannerView.setPadding(dp(12), dp(8), dp(12), dp(8));
        shareBannerView.setBackground(roundBox(cPrimaryContainer, 0, 12));
        shareBannerView.setVisibility(View.GONE);
        LinearLayout.LayoutParams sbLp = new LinearLayout.LayoutParams(MATCH, WRAP);
        sbLp.bottomMargin = dp(10);
        sendCard.addView(shareBannerView, sbLp);

        // Full-width Hero Primary CTA: 发照片 / 文件 + inline target status
        sendPickTile = new LinearLayout(this);
        sendPickTile.setOrientation(LinearLayout.HORIZONTAL);
        sendPickTile.setGravity(Gravity.CENTER_VERTICAL);
        sendPickTile.setPadding(dp(18), dp(18), dp(16), dp(18));
        sendPickTile.setBackground(ripple(roundBox(cPrimaryPressed, 0, 18), 18));
        sendPickTile.setOnClickListener(v -> pickFiles());

        LinearLayout heroCol = new LinearLayout(this);
        heroCol.setOrientation(LinearLayout.VERTICAL);

        TextView heroTitle = label("发照片 / 文件", 16, cOnPrimary, true);
        heroCol.addView(heroTitle, new LinearLayout.LayoutParams(MATCH, WRAP));

        LinearLayout heroSubRow = new LinearLayout(this);
        heroSubRow.setOrientation(LinearLayout.HORIZONTAL);
        heroSubRow.setGravity(Gravity.CENTER_VERTICAL);
        LinearLayout.LayoutParams hsrLp = new LinearLayout.LayoutParams(MATCH, WRAP);
        hsrLp.topMargin = dp(4);

        recvPulseDot = new View(this);
        recvPulseDot.setBackground(oval(Color.WHITE));
        LinearLayout.LayoutParams dotLp = new LinearLayout.LayoutParams(dp(7), dp(7));
        dotLp.rightMargin = dp(6);
        heroSubRow.addView(recvPulseDot, dotLp);

        sendTargetTitle = label("直发给 未选择设备", 12, Color.parseColor("#E6FFFFFF"), false);
        sendTargetTitle.setSingleLine(true);
        sendTargetTitle.setEllipsize(TextUtils.TruncateAt.END);
        heroSubRow.addView(sendTargetTitle, new LinearLayout.LayoutParams(0, WRAP, 1f));
        heroCol.addView(heroSubRow, hsrLp);

        sendPickTile.addView(heroCol, new LinearLayout.LayoutParams(0, WRAP, 1f));

        FrameLayout heroIconBox = new FrameLayout(this);
        heroIconBox.setBackground(roundBox(Color.parseColor("#29FFFFFF"), 0, 12));
        ImageView heroIv = new ImageView(this);
        heroIv.setImageDrawable(new IconDrawable(IconDrawable.UPLOAD, cOnPrimary));
        heroIconBox.addView(heroIv, new FrameLayout.LayoutParams(dp(22), dp(22), Gravity.CENTER));
        LinearLayout.LayoutParams hiLp = new LinearLayout.LayoutParams(dp(42), dp(42));
        hiLp.leftMargin = dp(12);
        sendPickTile.addView(heroIconBox, hiLp);

        // Compact 3-button secondary row: 发剪贴板 | 发文字 | 发文件夹 (NO equal 2x2 grid)
        LinearLayout secRow = new LinearLayout(this);
        secRow.setOrientation(LinearLayout.HORIZONTAL);
        LinearLayout.LayoutParams srRowLp = new LinearLayout.LayoutParams(MATCH, WRAP);
        srRowLp.topMargin = dp(10);

        sendClipTile = compactActionChip("发剪贴板", IconDrawable.CLIP, v -> sendClipboard());
        LinearLayout.LayoutParams sc1Lp = new LinearLayout.LayoutParams(0, WRAP, 1f);
        sc1Lp.rightMargin = dp(8);
        secRow.addView(sendClipTile, sc1Lp);

        sendTextTile = compactActionChip("发文字", IconDrawable.TEXT, v -> showSendTextDialog(""));
        LinearLayout.LayoutParams sc2Lp = new LinearLayout.LayoutParams(0, WRAP, 1f);
        sc2Lp.rightMargin = dp(8);
        secRow.addView(sendTextTile, sc2Lp);

        sendFolderTile = compactActionChip("发文件夹", IconDrawable.FOLDER, v -> pickFolder());
        secRow.addView(sendFolderTile, new LinearLayout.LayoutParams(0, WRAP, 1f));

        // Keep invisible holder for legacy status badge ref if needed
        sendRecvStatusBtn = label("", 12, cOnPrimary, false);
        sendRecvStatusBtn.setVisibility(View.GONE);

        LinearLayout sendInnerWrap = new LinearLayout(this);
        sendInnerWrap.setOrientation(LinearLayout.VERTICAL);
        sendInnerWrap.addView(sendPickTile, new LinearLayout.LayoutParams(MATCH, WRAP));
        sendInnerWrap.addView(secRow, srRowLp);

        // Smart Clipboard Suggestion Pill (shown only when non-empty clipboard text detected)
        homeClipBar = new LinearLayout(this);
        homeClipBar.setOrientation(LinearLayout.HORIZONTAL);
        homeClipBar.setGravity(Gravity.CENTER_VERTICAL);
        homeClipBar.setPadding(dp(13), dp(10), dp(13), dp(10));
        homeClipBar.setBackground(ripple(roundBox(cPrimaryContainer, 0, 12), 12));
        homeClipBar.setVisibility(View.GONE);
        homeClipBar.setOnClickListener(v -> sendClipboard());

        homeClipText = label("", 12, cOnPrimaryContainer, true);
        homeClipText.setSingleLine(true);
        homeClipText.setEllipsize(TextUtils.TruncateAt.END);
        homeClipBar.addView(homeClipText, new LinearLayout.LayoutParams(0, WRAP, 1f));

        LinearLayout clipSendBadge = new LinearLayout(this);
        clipSendBadge.setOrientation(LinearLayout.HORIZONTAL);
        clipSendBadge.setGravity(Gravity.CENTER_VERTICAL);
        ImageView clipSendIv = new ImageView(this);
        clipSendIv.setImageDrawable(new IconDrawable(IconDrawable.SEND, cOnPrimaryContainer));
        LinearLayout.LayoutParams csIvLp = new LinearLayout.LayoutParams(dp(14), dp(14));
        csIvLp.rightMargin = dp(4);
        clipSendBadge.addView(clipSendIv, csIvLp);
        clipSendBadge.addView(label("一键发送", 12, cOnPrimaryContainer, true), new LinearLayout.LayoutParams(WRAP, WRAP));
        LinearLayout.LayoutParams csbLp = new LinearLayout.LayoutParams(WRAP, WRAP);
        csbLp.leftMargin = dp(8);
        homeClipBar.addView(clipSendBadge, csbLp);

        LinearLayout.LayoutParams cbBarLp = new LinearLayout.LayoutParams(MATCH, WRAP);
        cbBarLp.topMargin = dp(10);
        sendInnerWrap.addView(homeClipBar, cbBarLp);

        // Result feedback banner (hidden when idle)
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
        sendInnerWrap.addView(sendResultCard, srLp);

        sendCard.addView(sendInnerWrap, new LinearLayout.LayoutParams(MATCH, WRAP));

        LinearLayout.LayoutParams scLp = new LinearLayout.LayoutParams(MATCH, WRAP);
        scLp.bottomMargin = dp(14);
        root.addView(sendCard, scLp);

        // 3. Value-First Unpaired Onboarding Card
        unpairedCard = new LinearLayout(this);
        unpairedCard.setOrientation(LinearLayout.VERTICAL);
        unpairedCard.setGravity(Gravity.CENTER_HORIZONTAL);
        unpairedCard.setPadding(dp(20), dp(18), dp(20), dp(18));
        unpairedCard.setBackground(roundBox(cCard, cBorder, 16));

        TextView upReady = label("● 本机已就绪 · 自动发现同 Wi-Fi 设备", 12, cOnPrimaryContainer, true);
        upReady.setPadding(dp(12), dp(6), dp(12), dp(6));
        upReady.setBackground(roundBox(cPrimaryContainer, 0, 99));
        LinearLayout.LayoutParams urLp = new LinearLayout.LayoutParams(WRAP, WRAP);
        urLp.bottomMargin = dp(12);
        unpairedCard.addView(upReady, urLp);

        TextView upTitle = label("先配对一台电脑", 15, cOnSurface, true);
        upTitle.setGravity(Gravity.CENTER);
        unpairedCard.addView(upTitle, new LinearLayout.LayoutParams(WRAP, WRAP));

        TextView upSub = label("在电脑轻渡点击「添加设备」，手机扫一下即可跨网免确认互传；同局域网设备也会自动出现在上方。", 12, cOnSurfaceVariant, false);
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

        TextView manualLink = label("输入 6 位码", 12, cPrimary, true);
        manualLink.setPadding(dp(8), dp(4), dp(8), dp(4));
        manualLink.setOnClickListener(v -> openPairSheet(2));
        upLinks.addView(manualLink, new LinearLayout.LayoutParams(WRAP, WRAP));

        TextView dotSep = label("·", 12, cOnSurfaceVariant, false);
        dotSep.setPadding(dp(4), 0, dp(4), 0);
        upLinks.addView(dotSep, new LinearLayout.LayoutParams(WRAP, WRAP));

        TextView tempLink = label("网页临时快传", 12, cPrimary, true);
        tempLink.setPadding(dp(8), dp(4), dp(8), dp(4));
        tempLink.setOnClickListener(v -> openTempShareSheet());
        upLinks.addView(tempLink, new LinearLayout.LayoutParams(WRAP, WRAP));

        TextView dotSep2 = label("·", 12, cOnSurfaceVariant, false);
        dotSep2.setPadding(dp(4), 0, dp(4), 0);
        upLinks.addView(dotSep2, new LinearLayout.LayoutParams(WRAP, WRAP));

        TextView lanLink = label("刷新局域网", 12, cOnSurfaceVariant, false);
        lanLink.setPadding(dp(8), dp(4), dp(8), dp(4));
        lanLink.setOnClickListener(v -> discover());
        upLinks.addView(lanLink, new LinearLayout.LayoutParams(WRAP, WRAP));
        unpairedCard.addView(upLinks, ulLp);

        LinearLayout.LayoutParams ucLp = new LinearLayout.LayoutParams(MATCH, WRAP);
        ucLp.bottomMargin = dp(14);
        root.addView(unpairedCard, ucLp);

        // 4. Compact Latest-1 Activity Card (no empty table clutter on Home!)
        View spacer = new View(this);
        root.addView(spacer, new LinearLayout.LayoutParams(MATCH, 0, 1f));

        homeLatestCard = new LinearLayout(this);
        homeLatestCard.setOrientation(LinearLayout.VERTICAL);
        homeLatestCard.setPadding(dp(16), dp(16), dp(16), dp(16));
        homeLatestCard.setBackground(roundBox(cCard, cBorder, 16));
        homeLatestCard.setVisibility(View.GONE);

        LinearLayout hlTop = new LinearLayout(this);
        hlTop.setOrientation(LinearLayout.HORIZONTAL);
        hlTop.setGravity(Gravity.CENTER_VERTICAL);
        hlTop.addView(label("最近一条", 12, cOnSurfaceVariant, true), new LinearLayout.LayoutParams(0, WRAP, 1f));
        TextView hlAll = label("全部记录 ›", 12, cPrimary, true);
        hlAll.setPadding(dp(6), dp(2), 0, dp(2));
        hlAll.setOnClickListener(v -> showPage(1));
        hlTop.addView(hlAll, new LinearLayout.LayoutParams(WRAP, WRAP));
        homeLatestCard.addView(hlTop, new LinearLayout.LayoutParams(MATCH, WRAP));

        LinearLayout hlRow = new LinearLayout(this);
        hlRow.setOrientation(LinearLayout.HORIZONTAL);
        hlRow.setGravity(Gravity.CENTER_VERTICAL);
        LinearLayout.LayoutParams hlrLp = new LinearLayout.LayoutParams(MATCH, WRAP);
        hlrLp.topMargin = dp(8);

        LinearLayout hlCol = new LinearLayout(this);
        hlCol.setOrientation(LinearLayout.VERTICAL);
        homeLatestTitle = label("", 13, cOnSurface, true);
        homeLatestTitle.setSingleLine(true);
        homeLatestTitle.setEllipsize(TextUtils.TruncateAt.MIDDLE);
        hlCol.addView(homeLatestTitle, new LinearLayout.LayoutParams(MATCH, WRAP));

        homeLatestSub = label("", 12, cOnSurfaceVariant, false);
        homeLatestSub.setSingleLine(true);
        homeLatestSub.setEllipsize(TextUtils.TruncateAt.END);
        LinearLayout.LayoutParams hlsLp = new LinearLayout.LayoutParams(MATCH, WRAP);
        hlsLp.topMargin = dp(2);
        hlCol.addView(homeLatestSub, hlsLp);
        hlRow.addView(hlCol, new LinearLayout.LayoutParams(0, WRAP, 1f));

        homeLatestBtn = smallOutlineBtn("打开", null);
        LinearLayout.LayoutParams hlbLp = new LinearLayout.LayoutParams(WRAP, dp(32));
        hlbLp.leftMargin = dp(10);
        hlRow.addView(homeLatestBtn, hlbLp);

        homeLatestCard.addView(hlRow, hlrLp);
        root.addView(homeLatestCard, new LinearLayout.LayoutParams(MATCH, WRAP));

        renderPeerSection();
        refreshReceiveUi();
        refreshDownloadDirUi();

        sv.addView(root, new FrameLayout.LayoutParams(MATCH, WRAP));
        return sv;
    }

    // ================= Page 1: Dedicated History Ledger =================

    private ScrollView buildHistoryPage() {
        ScrollView sv = new ScrollView(this);
        sv.setFillViewport(true);

        LinearLayout root = new LinearLayout(this);
        root.setOrientation(LinearLayout.VERTICAL);
        root.setPadding(dp(18), dp(10), dp(18), dp(24));

        LinearLayout recHdr = new LinearLayout(this);
        recHdr.setOrientation(LinearLayout.HORIZONTAL);
        recHdr.setGravity(Gravity.CENTER_VERTICAL);
        recHdr.setPadding(0, 0, 0, dp(10));

        String[] fKeys = {"all", "recv", "send"};
        String[] fNames = {"全部", "已收到", "已发出"};
        for (int i = 0; i < 3; i++) {
            final String fk = fKeys[i];
            TextView tv = label(fNames[i], 12, cOnSurfaceVariant, true);
            tv.setPadding(dp(12), dp(6), dp(12), dp(6));
            tv.setOnClickListener(v -> {
                activeFilter = fk;
                renderFilterTabs();
                renderRecentList();
            });
            filterTabs[i] = tv;
            LinearLayout.LayoutParams ftLp = new LinearLayout.LayoutParams(WRAP, WRAP);
            ftLp.rightMargin = dp(8);
            recHdr.addView(tv, ftLp);
        }
        recHdr.addView(new View(this), new LinearLayout.LayoutParams(0, 1, 1f));
        TextView dirHint = label("保存目录", 12, cOnSurfaceVariant, false);
        dirHint.setOnClickListener(v -> toast("收到的文件保存在" + currentDownloadDisplayName()));
        recHdr.addView(dirHint, new LinearLayout.LayoutParams(WRAP, WRAP));
        root.addView(recHdr, new LinearLayout.LayoutParams(MATCH, WRAP));

        LinearLayout listCard = new LinearLayout(this);
        listCard.setOrientation(LinearLayout.VERTICAL);
        listCard.setBackground(roundBox(cCard, cBorder, 16));
        listCard.setPadding(dp(4), dp(4), dp(4), dp(4));

        recentListView = new LinearLayout(this);
        recentListView.setOrientation(LinearLayout.VERTICAL);
        listCard.addView(recentListView, new LinearLayout.LayoutParams(MATCH, WRAP));

        recentEmptyView = new LinearLayout(this);
        recentEmptyView.setOrientation(LinearLayout.VERTICAL);
        recentEmptyView.setGravity(Gravity.CENTER_HORIZONTAL);
        recentEmptyView.setPadding(dp(16), dp(42), dp(16), dp(42));
        TextView empTitle = label("还没有传输记录", 14, cOnSurface, true);
        recentEmptyView.addView(empTitle, new LinearLayout.LayoutParams(WRAP, WRAP));
        recentEmptySub = label("收到的文件会自动放进系统 Download 文件夹。", 12, cOnSurfaceVariant, false);
        recentEmptySub.setGravity(Gravity.CENTER);
        LinearLayout.LayoutParams esLp = new LinearLayout.LayoutParams(WRAP, WRAP);
        esLp.topMargin = dp(4);
        recentEmptyView.addView(recentEmptySub, esLp);
        listCard.addView(recentEmptyView, new LinearLayout.LayoutParams(MATCH, WRAP));

        root.addView(listCard, new LinearLayout.LayoutParams(MATCH, WRAP));

        renderFilterTabs();
        renderRecentList();

        sv.addView(root, new FrameLayout.LayoutParams(MATCH, WRAP));
        return sv;
    }

    private LinearLayout compactActionChip(String text, int iconKind, View.OnClickListener click) {
        LinearLayout tile = new LinearLayout(this);
        tile.setOrientation(LinearLayout.HORIZONTAL);
        tile.setGravity(Gravity.CENTER);
        tile.setPadding(dp(8), dp(11), dp(8), dp(11));
        tile.setBackground(ripple(roundBox(cCard, cBorder, 12), 12));
        tile.setOnClickListener(click);

        ImageView iv = new ImageView(this);
        iv.setImageDrawable(new IconDrawable(iconKind, cOnSurface));
        LinearLayout.LayoutParams ivLp = new LinearLayout.LayoutParams(dp(15), dp(15));
        ivLp.rightMargin = dp(5);
        tile.addView(iv, ivLp);

        TextView tv = label(text, 12, cOnSurface, true);
        tv.setSingleLine(true);
        tile.addView(tv, new LinearLayout.LayoutParams(WRAP, WRAP));
        return tile;
    }

    // ================= Page 1: Devices (Saved Peers & LAN Discovery) =================

    private ScrollView buildDevicesPage() {
        ScrollView sv = new ScrollView(this);
        sv.setFillViewport(true);

        LinearLayout root = new LinearLayout(this);
        root.setOrientation(LinearLayout.VERTICAL);
        root.setPadding(dp(20), dp(10), dp(20), dp(32));

        LinearLayout actions = new LinearLayout(this);
        actions.setOrientation(LinearLayout.HORIZONTAL);
        actions.setGravity(Gravity.CENTER_VERTICAL);

        Button scanBtn = smallFilledBtn("扫码配对", cPrimaryPressed, cOnPrimary, v -> openPairSheet(0));
        scanBtn.setSingleLine(true);
        LinearLayout.LayoutParams b1Lp = new LinearLayout.LayoutParams(0, dp(38), 1f);
        b1Lp.rightMargin = dp(8);
        actions.addView(scanBtn, b1Lp);

        Button codeBtn = smallOutlineBtn("配对码", v -> openPairSheet(2));
        codeBtn.setSingleLine(true);
        LinearLayout.LayoutParams b2Lp = new LinearLayout.LayoutParams(0, dp(38), 1f);
        b2Lp.rightMargin = dp(8);
        actions.addView(codeBtn, b2Lp);

        devicesLanBtn = smallOutlineBtn("搜索局域网", v -> discover());
        devicesLanBtn.setSingleLine(true);
        actions.addView(devicesLanBtn, new LinearLayout.LayoutParams(0, dp(38), 1f));

        LinearLayout.LayoutParams acLp = new LinearLayout.LayoutParams(MATCH, WRAP);
        acLp.bottomMargin = dp(18);
        root.addView(actions, acLp);

        TextView savedHdr = label("已保存的设备", 12, cOnSurfaceVariant, true);
        savedHdr.setPadding(dp(4), 0, dp(4), dp(8));
        root.addView(savedHdr, new LinearLayout.LayoutParams(MATCH, WRAP));

        LinearLayout savedCard = new LinearLayout(this);
        savedCard.setOrientation(LinearLayout.VERTICAL);
        savedCard.setBackground(roundBox(cCard, cBorder, 16));

        savedDevicesList = new LinearLayout(this);
        savedDevicesList.setOrientation(LinearLayout.VERTICAL);
        savedCard.addView(savedDevicesList, new LinearLayout.LayoutParams(MATCH, WRAP));

        savedDevicesEmpty = label("还没有已保存的设备\n点击上方「扫码配对」或「配对码」添加", 13, cOnSurfaceVariant, false);
        savedDevicesEmpty.setGravity(Gravity.CENTER);
        savedDevicesEmpty.setPadding(dp(18), dp(24), dp(18), dp(24));
        savedCard.addView(savedDevicesEmpty, new LinearLayout.LayoutParams(MATCH, WRAP));

        LinearLayout.LayoutParams sWrapLp = new LinearLayout.LayoutParams(MATCH, WRAP);
        sWrapLp.bottomMargin = dp(20);
        root.addView(savedCard, sWrapLp);

        TextView lanHdr = label("同局域网设备", 12, cOnSurfaceVariant, true);
        lanHdr.setPadding(dp(4), 0, dp(4), dp(8));
        root.addView(lanHdr, new LinearLayout.LayoutParams(MATCH, WRAP));

        LinearLayout lanCard = new LinearLayout(this);
        lanCard.setOrientation(LinearLayout.VERTICAL);
        lanCard.setBackground(roundBox(cCard, cBorder, 16));

        lanDevicesList = new LinearLayout(this);
        lanDevicesList.setOrientation(LinearLayout.VERTICAL);
        lanCard.addView(lanDevicesList, new LinearLayout.LayoutParams(MATCH, WRAP));

        lanDevicesEmpty = label("点击上方「搜索局域网」查找同一 Wi-Fi 下可直连设备", 13, cOnSurfaceVariant, false);
        lanDevicesEmpty.setGravity(Gravity.CENTER);
        lanDevicesEmpty.setPadding(dp(18), dp(20), dp(18), dp(20));
        lanCard.addView(lanDevicesEmpty, new LinearLayout.LayoutParams(MATCH, WRAP));

        root.addView(lanCard, new LinearLayout.LayoutParams(MATCH, WRAP));

        sv.addView(root, new FrameLayout.LayoutParams(MATCH, WRAP));
        renderDevicesPage();
        return sv;
    }

    private void renderDevicesPage() {
        if (savedDevicesList == null || savedDevicesEmpty == null
                || lanDevicesList == null || lanDevicesEmpty == null) {
            return;
        }
        if (devicesLanBtn != null) {
            devicesLanBtn.setText(lanSearching ? "搜索中…" : "搜索局域网");
            devicesLanBtn.setEnabled(!lanSearching);
        }

        savedDevicesList.removeAllViews();
        if (pairedEntries.isEmpty()) {
            savedDevicesList.setVisibility(View.GONE);
            savedDevicesEmpty.setVisibility(View.VISIBLE);
        } else {
            savedDevicesList.setVisibility(View.VISIBLE);
            savedDevicesEmpty.setVisibility(View.GONE);
            for (int i = 0; i < pairedEntries.size(); i++) {
                final PeerEntry entry = pairedEntries.get(i);
                final boolean sel = entry.value.equals(selectedTarget);

                LinearLayout item = new LinearLayout(this);
                item.setOrientation(LinearLayout.VERTICAL);
                item.setPadding(dp(16), dp(14), dp(16), dp(14));

                LinearLayout hdr = new LinearLayout(this);
                hdr.setOrientation(LinearLayout.HORIZONTAL);
                hdr.setGravity(Gravity.CENTER_VERTICAL);

                TextView nmTv = label(entry.name, 14, cOnSurface, true);
                nmTv.setSingleLine(true);
                nmTv.setEllipsize(TextUtils.TruncateAt.END);
                hdr.addView(nmTv, new LinearLayout.LayoutParams(0, WRAP, 1f));

                String stLabel = "离线";
                int stBg = cSurfaceAlt;
                int stFg = cOnSurfaceVariant;
                if (lanOnlineSet.contains(entry.value)) {
                    stLabel = "局域网";
                    stBg = cPrimaryContainer;
                    stFg = cOnPrimaryContainer;
                } else if ("online".equals(signalState) && cloudOnlineSet.contains(entry.value)) {
                    stLabel = "云端在线";
                    stBg = cPrimaryContainer;
                    stFg = cOnPrimaryContainer;
                }
                TextView stBadge = label(stLabel, 10, stFg, true);
                stBadge.setPadding(dp(8), dp(2), dp(8), dp(2));
                stBadge.setBackground(roundBox(stBg, 0, 99));
                LinearLayout.LayoutParams sLp = new LinearLayout.LayoutParams(WRAP, WRAP);
                sLp.leftMargin = dp(8);
                hdr.addView(stBadge, sLp);

                if (sel) {
                    TextView badge = label("当前目标", 10, cOnPrimaryContainer, true);
                    badge.setPadding(dp(8), dp(2), dp(8), dp(2));
                    badge.setBackground(roundBox(cPrimaryContainer, 0, 99));
                    LinearLayout.LayoutParams bLp = new LinearLayout.LayoutParams(WRAP, WRAP);
                    bLp.leftMargin = dp(8);
                    hdr.addView(badge, bLp);
                }
                item.addView(hdr, new LinearLayout.LayoutParams(MATCH, WRAP));

                String shortId = entry.value.length() > 16
                        ? entry.value.substring(0, 16) + "…"
                        : entry.value;
                TextView idTv = label("ID · " + shortId + "（点击复制）", 11, cOnSurfaceVariant, false);
                idTv.setTypeface(Typeface.MONOSPACE);
                idTv.setPadding(0, dp(3), 0, dp(2));
                idTv.setOnClickListener(v -> copyText("设备 ID", entry.value, "已复制对方设备 ID"));
                item.addView(idTv, new LinearLayout.LayoutParams(WRAP, WRAP));

                LinearLayout rowBtns = new LinearLayout(this);
                rowBtns.setOrientation(LinearLayout.HORIZONTAL);
                rowBtns.setGravity(Gravity.CENTER_VERTICAL);

                Button useBtn = sel
                        ? smallOutlineBtn("在传输页发送", v -> {
                            selectedTarget = entry.value;
                            userPickedTarget = true;
                            saveLastPeer(selectedTarget);
                            renderPeerSection();
                            showPage(0);
                        })
                        : smallFilledBtn("选为目标", cPrimaryPressed, cOnPrimary, v -> {
                            selectedTarget = entry.value;
                            userPickedTarget = true;
                            saveLastPeer(selectedTarget);
                            renderPeerSection();
                            showPage(0);
                        });
                LinearLayout.LayoutParams uLp = new LinearLayout.LayoutParams(WRAP, dp(32));
                uLp.rightMargin = dp(8);
                rowBtns.addView(useBtn, uLp);

                Button renBtn = smallOutlineBtn("重命名", v -> showRenamePeerDialog(entry));
                LinearLayout.LayoutParams rLp = new LinearLayout.LayoutParams(WRAP, dp(32));
                rLp.rightMargin = dp(8);
                rowBtns.addView(renBtn, rLp);

                Button delBtn = smallOutlineBtn("删除", v -> confirmRemovePeer(entry));
                delBtn.setTextColor(cError);
                rowBtns.addView(delBtn, new LinearLayout.LayoutParams(WRAP, dp(32)));

                LinearLayout.LayoutParams rbLp = new LinearLayout.LayoutParams(MATCH, WRAP);
                rbLp.topMargin = dp(10);
                item.addView(rowBtns, rbLp);

                savedDevicesList.addView(item, new LinearLayout.LayoutParams(MATCH, WRAP));
                if (i < pairedEntries.size() - 1) {
                    savedDevicesList.addView(hairline(), new LinearLayout.LayoutParams(MATCH, 1));
                }
            }
        }

        lanDevicesList.removeAllViews();
        if (lanEntries.isEmpty()) {
            lanDevicesList.setVisibility(View.GONE);
            lanDevicesEmpty.setVisibility(View.VISIBLE);
        } else {
            lanDevicesList.setVisibility(View.VISIBLE);
            lanDevicesEmpty.setVisibility(View.GONE);
            for (int i = 0; i < lanEntries.size(); i++) {
                final PeerEntry entry = lanEntries.get(i);
                final boolean sel = entry.value.equals(selectedTarget);

                LinearLayout row = new LinearLayout(this);
                row.setOrientation(LinearLayout.HORIZONTAL);
                row.setGravity(Gravity.CENTER_VERTICAL);
                row.setPadding(dp(16), dp(13), dp(16), dp(13));

                LinearLayout col = new LinearLayout(this);
                col.setOrientation(LinearLayout.VERTICAL);
                TextView nmTv = label(entry.name, 14, cOnSurface, true);
                nmTv.setSingleLine(true);
                nmTv.setEllipsize(TextUtils.TruncateAt.END);
                col.addView(nmTv, new LinearLayout.LayoutParams(MATCH, WRAP));

                TextView addrTv = label(entry.value, 11, cOnSurfaceVariant, false);
                addrTv.setTypeface(Typeface.MONOSPACE);
                LinearLayout.LayoutParams aLp = new LinearLayout.LayoutParams(MATCH, WRAP);
                aLp.topMargin = dp(2);
                col.addView(addrTv, aLp);
                row.addView(col, new LinearLayout.LayoutParams(0, WRAP, 1f));

                Button useBtn = smallOutlineBtn(sel ? "去传输" : "选为目标", v -> {
                    selectedTarget = entry.value;
                    userPickedTarget = true;
                    saveLastPeer(selectedTarget);
                    renderPeerSection();
                    showPage(0);
                });
                LinearLayout.LayoutParams uLp = new LinearLayout.LayoutParams(WRAP, dp(32));
                uLp.leftMargin = dp(10);
                row.addView(useBtn, uLp);

                lanDevicesList.addView(row, new LinearLayout.LayoutParams(MATCH, WRAP));
                if (i < lanEntries.size() - 1) {
                    lanDevicesList.addView(hairline(), new LinearLayout.LayoutParams(MATCH, 1));
                }
            }
        }
    }

    private void showRenamePeerDialog(final PeerEntry entry) {
        final EditText input = modalInput("输入备注名（留空恢复默认）");
        input.setText(entry.name);
        input.setSelection(entry.name.length());

        FrameLayout wrap = new FrameLayout(this);
        wrap.setPadding(dp(20), dp(10), dp(20), 0);
        wrap.addView(input, new FrameLayout.LayoutParams(MATCH, dp(44)));

        new AlertDialog.Builder(this)
                .setTitle("重命名设备")
                .setView(wrap)
                .setNegativeButton("取消", null)
                .setPositiveButton("保存", (d, w) -> {
                    final String newName = trimToUtf8Bytes(input.getText().toString().trim(), 64);
                    ioPool.execute(() -> {
                        try {
                            Mobile.renamePairedPeer(pairedFile().getAbsolutePath(), entry.value, newName);
                            ui.post(() -> {
                                loadPairedPeers();
                                toast(newName.isEmpty() ? "已恢复默认显示名" : "已重命名为 " + newName);
                            });
                        } catch (Exception e) {
                            ui.post(() -> toast("重命名失败：" + e.getMessage()));
                        }
                    });
                })
                .show();
    }

    private void confirmRemovePeer(final PeerEntry entry) {
        new AlertDialog.Builder(this)
                .setTitle("删除已保存设备")
                .setMessage("确定要删除「" + entry.name + "」吗？删除后将无法自动互传，需重新扫码配对。")
                .setNegativeButton("取消", null)
                .setPositiveButton("删除", (d, w) -> ioPool.execute(() -> {
                    try {
                        Mobile.removePairedPeer(pairedFile().getAbsolutePath(), entry.value);
                        ui.post(() -> {
                            if (entry.value.equals(selectedTarget)) {
                                selectedTarget = "";
                                saveLastPeer("");
                            }
                            loadPairedPeers();
                            toast("已删除设备");
                        });
                    } catch (Exception e) {
                        ui.post(() -> toast("删除失败：" + e.getMessage()));
                    }
                }))
                .show();
    }

    // ================= Page 2: Settings (Local Device Info lives here) =================

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
        grp1.addView(settingActionRow("后台接收文件", "开启后其他设备可随时向此手机发送文件", setRecvBtn, null),
                new LinearLayout.LayoutParams(MATCH, WRAP));

        grp1.addView(hairline(), new LinearLayout.LayoutParams(MATCH, 1));

        grp1.addView(settingDownloadDirRow(), new LinearLayout.LayoutParams(MATCH, WRAP));

        grp1.addView(hairline(), new LinearLayout.LayoutParams(MATCH, 1));

        grp1.addView(settingServerRow(), new LinearLayout.LayoutParams(MATCH, WRAP));

        grp1.addView(hairline(), new LinearLayout.LayoutParams(MATCH, 1));

        Button batBtn = smallOutlineBtn("去设置", v -> requestBatteryExemption());
        grp1.addView(settingActionRow("后台保活白名单", "防止息屏后漏收电脑发来的文件", batBtn, v -> requestBatteryExemption()),
                new LinearLayout.LayoutParams(MATCH, WRAP));

        grp1.addView(hairline(), new LinearLayout.LayoutParams(MATCH, 1));

        grp1.addView(settingKeepAliveNotifRow(), new LinearLayout.LayoutParams(MATCH, WRAP));

        grp1.addView(hairline(), new LinearLayout.LayoutParams(MATCH, 1));

        setHideRecentsBtn = smallOutlineBtn("已关闭", v -> onHideRecentsToggleClicked());
        grp1.addView(settingActionRow("隐藏最近任务后台", "开启后从多任务列表隐藏卡片，防止误划清理", setHideRecentsBtn,
                        v -> onHideRecentsToggleClicked()),
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

        LinearLayout.LayoutParams g2Lp = new LinearLayout.LayoutParams(MATCH, WRAP);
        g2Lp.bottomMargin = dp(22);
        root.addView(grp2, g2Lp);

        // Group 3: 关于
        TextView aboutHdr = label("关于", 12, cOnSurfaceVariant, true);
        aboutHdr.setPadding(dp(4), 0, dp(4), dp(8));
        root.addView(aboutHdr, new LinearLayout.LayoutParams(MATCH, WRAP));

        LinearLayout grp3 = new LinearLayout(this);
        grp3.setOrientation(LinearLayout.VERTICAL);
        grp3.setBackground(roundBox(cCard, cBorder, 16));

        LinearLayout aboutRow = new LinearLayout(this);
        aboutRow.setOrientation(LinearLayout.HORIZONTAL);
        aboutRow.setGravity(Gravity.CENTER_VERTICAL);
        aboutRow.setPadding(dp(16), dp(14), dp(16), dp(14));

        ImageView mark = new ImageView(this);
        mark.setImageDrawable(new FerryMarkDrawable());
        LinearLayout.LayoutParams mkLp = new LinearLayout.LayoutParams(dp(40), dp(40));
        mkLp.rightMargin = dp(12);
        aboutRow.addView(mark, mkLp);

        LinearLayout aboutCol = new LinearLayout(this);
        aboutCol.setOrientation(LinearLayout.VERTICAL);
        aboutCol.addView(label("轻渡 Ferry", 15, cOnSurface, true), new LinearLayout.LayoutParams(WRAP, WRAP));
        String rawVer = Mobile.getVersion();
        String verText = (rawVer != null && !rawVer.trim().isEmpty())
                ? (rawVer.trim().startsWith("v") ? ("版本 " + rawVer.trim()) : ("版本 v" + rawVer.trim()))
                : "版本 v1.0.0";
        TextView verView = label(verText, 12, cOnSurfaceVariant, false);
        verView.setTypeface(Typeface.MONOSPACE);
        LinearLayout.LayoutParams vLp = new LinearLayout.LayoutParams(WRAP, WRAP);
        vLp.topMargin = dp(2);
        aboutCol.addView(verView, vLp);
        aboutRow.addView(aboutCol, new LinearLayout.LayoutParams(0, WRAP, 1f));
        grp3.addView(aboutRow, new LinearLayout.LayoutParams(MATCH, WRAP));

        grp3.addView(hairline(), new LinearLayout.LayoutParams(MATCH, 1));

        final String repoUrl = "https://github.com/zhitiaojun/ferry";
        TextView repoVal = label("github.com/zhitiaojun/ferry", 12, cPrimary, true);
        grp3.addView(settingInfoRow("项目地址", repoVal, v -> {
            try {
                startActivity(new Intent(Intent.ACTION_VIEW, Uri.parse(repoUrl)));
            } catch (Exception e) {
                copyText("项目地址", repoUrl, "已复制项目地址");
            }
        }), new LinearLayout.LayoutParams(MATCH, WRAP));

        LinearLayout.LayoutParams g3Lp = new LinearLayout.LayoutParams(MATCH, WRAP);
        g3Lp.bottomMargin = dp(18);
        root.addView(grp3, g3Lp);

        // Legacy pair file import fallback
        TextView importLegacyBtn = label("导入旧版配对文件 (.txt)", 12, cOnSurfaceVariant, false);
        importLegacyBtn.setGravity(Gravity.CENTER);
        importLegacyBtn.setPadding(dp(12), dp(10), dp(12), dp(10));
        importLegacyBtn.setBackground(ripple(roundBox(Color.TRANSPARENT, 0, 12), 12));
        importLegacyBtn.setOnClickListener(v -> pickPairFile());
        root.addView(importLegacyBtn, new LinearLayout.LayoutParams(MATCH, WRAP));

        refreshDownloadDirUi();
        refreshServerUi();
        refreshKeepAliveNotifUi();
        refreshHideRecentsUi();
        sv.addView(root, new FrameLayout.LayoutParams(MATCH, WRAP));
        return sv;
    }

    private LinearLayout settingServerRow() {
        LinearLayout row = new LinearLayout(this);
        row.setOrientation(LinearLayout.HORIZONTAL);
        row.setGravity(Gravity.CENTER_VERTICAL);
        row.setPadding(dp(16), dp(14), dp(16), dp(14));

        LinearLayout col = new LinearLayout(this);
        col.setOrientation(LinearLayout.VERTICAL);
        col.addView(label("服务器地址", 13, cOnSurface, true), new LinearLayout.LayoutParams(MATCH, WRAP));
        setServerSub = label("默认：" + TransferService.getServerHostLabel(this), 12, cOnSurfaceVariant, false);
        setServerSub.setSingleLine(true);
        setServerSub.setEllipsize(TextUtils.TruncateAt.MIDDLE);
        LinearLayout.LayoutParams sLp = new LinearLayout.LayoutParams(MATCH, WRAP);
        sLp.topMargin = dp(2);
        col.addView(setServerSub, sLp);
        row.addView(col, new LinearLayout.LayoutParams(0, WRAP, 1f));

        LinearLayout btns = new LinearLayout(this);
        btns.setOrientation(LinearLayout.HORIZONTAL);
        btns.setGravity(Gravity.CENTER_VERTICAL);

        setServerResetBtn = smallOutlineBtn("恢复默认", v -> resetServerConfig());
        setServerResetBtn.setVisibility(View.GONE);
        LinearLayout.LayoutParams rstLp = new LinearLayout.LayoutParams(WRAP, dp(30));
        rstLp.rightMargin = dp(6);
        btns.addView(setServerResetBtn, rstLp);

        Button changeBtn = smallOutlineBtn("修改", v -> showServerConfigDialog());
        btns.addView(changeBtn, new LinearLayout.LayoutParams(WRAP, dp(30)));

        LinearLayout.LayoutParams bLp = new LinearLayout.LayoutParams(WRAP, WRAP);
        bLp.leftMargin = dp(10);
        row.addView(btns, bLp);
        return row;
    }

    private void refreshServerUi() {
        boolean custom = TransferService.isCustomServer(this);
        String host = TransferService.getServerHostLabel(this);
        if (setServerSub != null) {
            String stun = TransferService.getStunServers(this);
            String labelTxt = custom ? ("自定义：" + host) : ("默认：" + host);
            if (!stun.isEmpty()) {
                labelTxt += " · STUN 已配置";
            }
            setServerSub.setText(labelTxt);
        }
        if (setServerResetBtn != null) {
            setServerResetBtn.setVisibility(custom ? View.VISIBLE : View.GONE);
        }
    }

    private void resetServerConfig() {
        TransferService.saveServerConfig(this, "", "", "");
        applyServerConfigChanged("已恢复默认服务器地址");
    }

    private void showServerConfigDialog() {
        LinearLayout box = new LinearLayout(this);
        box.setOrientation(LinearLayout.VERTICAL);
        box.setPadding(dp(20), dp(12), dp(20), dp(4));

        box.addView(label("信令服务 WebSocket 地址（wss://...）", 12, cOnSurfaceVariant, false),
                new LinearLayout.LayoutParams(MATCH, WRAP));
        final EditText sigIn = modalInput(TransferService.DEFAULT_SIGNAL_URL);
        sigIn.setText(TransferService.getSignalUrl(this));
        LinearLayout.LayoutParams i1 = new LinearLayout.LayoutParams(MATCH, dp(44));
        i1.topMargin = dp(4);
        i1.bottomMargin = dp(12);
        box.addView(sigIn, i1);

        box.addView(label("临时快传公网根地址（https://...）", 12, cOnSurfaceVariant, false),
                new LinearLayout.LayoutParams(MATCH, WRAP));
        final EditText webIn = modalInput(TransferService.DEFAULT_WEB_BASE_URL);
        webIn.setText(TransferService.getWebBaseUrl(this));
        LinearLayout.LayoutParams i2 = new LinearLayout.LayoutParams(MATCH, dp(44));
        i2.topMargin = dp(4);
        i2.bottomMargin = dp(12);
        box.addView(webIn, i2);

        box.addView(label("STUN 服务器（可选，多个用英文逗号分隔）", 12, cOnSurfaceVariant, false),
                new LinearLayout.LayoutParams(MATCH, WRAP));
        final EditText stunIn = modalInput("留空使用内置默认 STUN");
        stunIn.setText(TransferService.getStunServers(this));
        LinearLayout.LayoutParams i3 = new LinearLayout.LayoutParams(MATCH, dp(44));
        i3.topMargin = dp(4);
        box.addView(stunIn, i3);

        AlertDialog dlg = new AlertDialog.Builder(this)
                .setTitle("服务器地址设置")
                .setView(box)
                .setNegativeButton("取消", null)
                .setNeutralButton("恢复默认", (d, w) -> resetServerConfig())
                .setPositiveButton("保存", null)
                .create();
        dlg.setOnShowListener(d -> {
            Button pos = dlg.getButton(AlertDialog.BUTTON_POSITIVE);
            pos.setOnClickListener(v -> {
                String err = TransferService.saveServerConfig(
                        MainActivity.this,
                        sigIn.getText().toString(),
                        webIn.getText().toString(),
                        stunIn.getText().toString());
                if (!err.isEmpty()) {
                    toast(err);
                    return;
                }
                dlg.dismiss();
                applyServerConfigChanged("服务器地址已保存");
            });
        });
        dlg.show();
    }

    private void applyServerConfigChanged(final String msg) {
        refreshServerUi();
        refreshReceiveUi();
        if (recvRunning) {
            new Thread(() -> {
                try {
                    Mobile.stopReceiver();
                    Mobile.stopAutoReceive();
                } catch (Exception e) {
                    Log.w(TAG, "restart recv on server change", e);
                }
                ui.post(() -> {
                    startAllReceive();
                    toast(msg + "，正在重连");
                });
            }).start();
        } else {
            toast(msg);
        }
    }

    private LinearLayout settingDownloadDirRow() {
        LinearLayout row = new LinearLayout(this);
        row.setOrientation(LinearLayout.HORIZONTAL);
        row.setGravity(Gravity.CENTER_VERTICAL);
        row.setPadding(dp(16), dp(14), dp(16), dp(14));

        LinearLayout col = new LinearLayout(this);
        col.setOrientation(LinearLayout.VERTICAL);
        col.addView(label("下载目录", 13, cOnSurface, true), new LinearLayout.LayoutParams(MATCH, WRAP));
        setDownloadDirSub = label("默认：系统 Download 文件夹", 12, cOnSurfaceVariant, false);
        setDownloadDirSub.setSingleLine(true);
        setDownloadDirSub.setEllipsize(TextUtils.TruncateAt.MIDDLE);
        LinearLayout.LayoutParams sLp = new LinearLayout.LayoutParams(MATCH, WRAP);
        sLp.topMargin = dp(2);
        col.addView(setDownloadDirSub, sLp);
        row.addView(col, new LinearLayout.LayoutParams(0, WRAP, 1f));

        LinearLayout btns = new LinearLayout(this);
        btns.setOrientation(LinearLayout.HORIZONTAL);
        btns.setGravity(Gravity.CENTER_VERTICAL);

        setDownloadResetBtn = smallOutlineBtn("恢复默认", v -> resetDownloadDirectory());
        setDownloadResetBtn.setVisibility(View.GONE);
        LinearLayout.LayoutParams rstLp = new LinearLayout.LayoutParams(WRAP, dp(30));
        rstLp.rightMargin = dp(6);
        btns.addView(setDownloadResetBtn, rstLp);

        Button changeBtn = smallOutlineBtn("更改", v -> pickDownloadDirectory());
        btns.addView(changeBtn, new LinearLayout.LayoutParams(WRAP, dp(30)));

        LinearLayout.LayoutParams bLp = new LinearLayout.LayoutParams(WRAP, WRAP);
        bLp.leftMargin = dp(10);
        row.addView(btns, bLp);
        return row;
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
        refreshDownloadDirUi();
        refreshServerUi();
        refreshKeepAliveNotifUi();
        refreshHideRecentsUi();
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
                    String n = trimToUtf8Bytes(input.getText().toString().trim(), 64);
                    if (n.isEmpty()) {
                        n = defaultName();
                    }
                    final String finalName = n;
                    deviceName = finalName;
                    TransferService.prefs(this).edit().putString("deviceName", finalName).apply();
                    refreshSettingsInfo();
                    if (recvRunning) {
                        new Thread(() -> {
                            try {
                                Mobile.stopReceiver();
                                Mobile.stopAutoReceive();
                            } catch (Exception e) {
                                Log.w(TAG, "restart recv on rename", e);
                            }
                            ui.post(() -> {
                                startAllReceive();
                                toast("设备名称已更新为 " + finalName);
                            });
                        }).start();
                    } else {
                        toast("设备名称已更新为 " + finalName);
                    }
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

        final String myName = deviceName;
        new Thread(() -> {
            try {
                Mobile.waitPairCodeNamed(TransferService.getSignalUrl(MainActivity.this),
                        keyFile().getAbsolutePath(), pairedFile().getAbsolutePath(),
                        code, myName, PAIR_TTL_SEC * 1000L);
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
        final String myName = deviceName;
        if (peer.startsWith("ferry://")) {
            enterSubmitBtn.setEnabled(false);
            enterStatusView.setVisibility(View.VISIBLE);
            enterStatusView.setTextColor(cOnSurfaceVariant);
            enterStatusView.setText("正在验证二维码链接并配对…");
            new Thread(() -> {
                try {
                    Mobile.pairWithURINamed(TransferService.getSignalUrl(MainActivity.this),
                            keyFile().getAbsolutePath(), pairedFile().getAbsolutePath(), peer, myName);
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
                Mobile.pairWithCodeNamed(TransferService.getSignalUrl(MainActivity.this),
                        keyFile().getAbsolutePath(), pairedFile().getAbsolutePath(), code, peer, myName);
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
            final CameraCharacteristics cc = cm.getCameraCharacteristics(camId);
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
                    createScanSession(cam, cc);
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

    private static final float SCAN_DEFAULT_ZOOM = 1.5f;

    private void createScanSession(final CameraDevice cam, final CameraCharacteristics cc) {
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
            applyDefaultScanZoom(req, cc, SCAN_DEFAULT_ZOOM);

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

    private void applyDefaultScanZoom(CaptureRequest.Builder req, CameraCharacteristics cc, float targetZoom) {
        if (req == null || cc == null || targetZoom <= 1.0f) {
            return;
        }
        try {
            if (Build.VERSION.SDK_INT >= 30) {
                Range<Float> range = cc.get(CameraCharacteristics.CONTROL_ZOOM_RATIO_RANGE);
                if (range != null) {
                    float z = Math.max(range.getLower(), Math.min(targetZoom, range.getUpper()));
                    req.set(CaptureRequest.CONTROL_ZOOM_RATIO, z);
                    return;
                }
            }
            Float maxDigital = cc.get(CameraCharacteristics.SCALER_AVAILABLE_MAX_DIGITAL_ZOOM);
            Rect active = cc.get(CameraCharacteristics.SENSOR_INFO_ACTIVE_ARRAY_SIZE);
            if (maxDigital != null && active != null && maxDigital > 1.0f) {
                float z = Math.max(1.0f, Math.min(targetZoom, maxDigital));
                int cropW = Math.round(active.width() / z);
                int cropH = Math.round(active.height() / z);
                int dx = (active.width() - cropW) / 2;
                int dy = (active.height() - cropH) / 2;
                req.set(CaptureRequest.SCALER_CROP_REGION,
                        new Rect(active.left + dx, active.top + dy, active.left + dx + cropW, active.top + dy + cropH));
            }
        } catch (Throwable t) {
            Log.w(TAG, "applyDefaultScanZoom", t);
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
        if (payload != null && TEMP_SHARE_URL.matcher(payload.trim()).matches()) {
            closePairSheet();
            openTempJoinSheet(payload.trim());
            return;
        }
        if (payload.startsWith("ftpair://")) {
            new AlertDialog.Builder(this)
                    .setTitle("信任这台设备？")
                    .setMessage("设备 ID：" + shortPeerId(payload) + "\n\n导入后该设备可向你发送文件。")
                    .setNegativeButton("取消", (d, w) -> {
                        scanSucceeded.set(false);
                        if (pairDialog != null && pairDialog.isShowing() && pairMode == 0) {
                            startCameraIfReady();
                        }
                    })
                    .setPositiveButton("导入并信任", (d, w) -> {
                        if (scanStatusLabel != null) {
                            scanStatusLabel.setText("已识别二维码，正在导入…");
                        }
                        ioPool.execute(() -> {
                            try {
                                Mobile.pairImport(payload, pairedFile().getAbsolutePath());
                                ui.post(() -> {
                                    if (pairDialog != null && pairDialog.isShowing()) {
                                        toast("配对成功");
                                        loadPairedPeers();
                                        closePairSheet();
                                    }
                                });
                            } catch (Exception e) {
                                ui.post(() -> onScannedPairError(payload, e.getMessage()));
                            }
                        });
                    })
                    .setOnCancelListener(d -> {
                        scanSucceeded.set(false);
                        if (pairDialog != null && pairDialog.isShowing() && pairMode == 0) {
                            startCameraIfReady();
                        }
                    })
                    .show();
            return;
        }
        if (scanStatusLabel != null) {
            scanStatusLabel.setText("已识别二维码，正在配对…");
        }
        final String myName = deviceName;
        new Thread(() -> {
            try {
                Mobile.pairWithURINamed(TransferService.getSignalUrl(MainActivity.this),
                        keyFile().getAbsolutePath(), pairedFile().getAbsolutePath(), payload, myName);
                ui.post(() -> {
                    if (pairDialog != null && pairDialog.isShowing()) {
                        toast("配对成功");
                        loadPairedPeers();
                        closePairSheet();
                    }
                });
            } catch (Exception e) {
                ui.post(() -> onScannedPairError(payload, e.getMessage()));
            }
        }).start();
    }

    private void onScannedPairError(String payload, String msg) {
        lastRejectedQR = payload;
        lastRejectMs = SystemClock.uptimeMillis();
        scanSucceeded.set(false);
        String errMsg = (msg == null || msg.trim().isEmpty()) ? "配对失败" : msg;
        if (scanStatusLabel != null) {
            scanStatusLabel.setText(errMsg);
        }
        toast(errMsg);
        if (pairDialog != null && pairDialog.isShowing() && pairMode == 0) {
            startCameraIfReady();
        }
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
        Intent i = new Intent(this, TransferService.class).setAction(TransferService.ACTION_START_RECV);
        if (Build.VERSION.SDK_INT >= 26) {
            startForegroundService(i);
        } else {
            startService(i);
        }
    }

    private void stopAllReceive() {
        if (setRecvBtn != null) {
            setRecvBtn.setEnabled(false);
        }
        Intent i = new Intent(this, TransferService.class).setAction(TransferService.ACTION_STOP_RECV);
        startService(i);
        toast("正在关闭后台接收…");
    }

    private final Runnable tempPollTask = new Runnable() {
        @Override
        public void run() {
            if (tempShareDialog == null || !tempShareDialog.isShowing()) {
                return;
            }
            ioPool.execute(() -> {
                try {
                    String raw = Mobile.tempShareStatus();
                    JSONObject o = new JSONObject(raw);
                    final boolean active = o.optBoolean("active", false);
                    final int ttl = o.optInt("ttl_s", 0);
                    final int upCount = o.optInt("uploaded_count", 0);
                    final String last = o.optString("last_file", "");
                    ui.post(() -> {
                        if (tempShareDialog == null || !tempShareDialog.isShowing()) {
                            return;
                        }
                        if (!active) {
                            webRunning = false;
                            webShareUrl = "";
                            refreshReceiveUi();
                            return;
                        }
                        if (tempTtlTv != null) {
                            tempTtlTv.setText(String.format(Locale.US, "10 分钟有效 · 剩余 %02d:%02d", ttl / 60, ttl % 60));
                        }
                        if (tempUploadTv != null && upCount > 0) {
                            tempUploadTv.setText("已收到 " + upCount + " 个文件" + (last.isEmpty() ? "" : " · " + last));
                            tempUploadTv.setVisibility(View.VISIBLE);
                            pollReceivedOnce();
                        }
                    });
                } catch (Exception ignored) {
                }
            });
            ui.postDelayed(this, 2000);
        }
    };

    private void openTempShareSheet() {
        if (tempShareDialog != null && tempShareDialog.isShowing()) {
            tempShareDialog.dismiss();
        }
        JSONObject activeStatus = null;
        try {
            JSONObject o = new JSONObject(Mobile.tempShareStatus());
            if (o.optBoolean("active", false)) {
                activeStatus = o;
            }
        } catch (Exception ignored) {
        }

        Dialog dlg = new Dialog(this);
        dlg.requestWindowFeature(Window.FEATURE_NO_TITLE);
        dlg.setCanceledOnTouchOutside(true);
        tempShareDialog = dlg;

        LinearLayout sheet = new LinearLayout(this);
        sheet.setOrientation(LinearLayout.VERTICAL);
        sheet.setPadding(dp(20), dp(18), dp(20), dp(20));
        sheet.setBackground(roundBox(cSheetBg, cBorder, 20));

        LinearLayout topRow = new LinearLayout(this);
        topRow.setOrientation(LinearLayout.HORIZONTAL);
        topRow.setGravity(Gravity.CENTER_VERTICAL);
        topRow.addView(label("临时快传", 18, cOnSurface, true), new LinearLayout.LayoutParams(0, WRAP, 1f));
        TextView closeTv = label("关闭", 13, cOnSurfaceVariant, false);
        closeTv.setPadding(dp(8), dp(4), dp(8), dp(4));
        closeTv.setOnClickListener(v -> dlg.dismiss());
        topRow.addView(closeTv, new LinearLayout.LayoutParams(WRAP, WRAP));
        sheet.addView(topRow, new LinearLayout.LayoutParams(MATCH, WRAP));

        if (activeStatus == null) {
            TextView sub = label("生成 10 分钟有效的 HTTPS 二维码与 6 位码，对方用浏览器或轻渡扫码即可收发，不留设备记录。", 12, cOnSurfaceVariant, false);
            LinearLayout.LayoutParams sLp = new LinearLayout.LayoutParams(MATCH, WRAP);
            sLp.topMargin = dp(6);
            sLp.bottomMargin = dp(16);
            sheet.addView(sub, sLp);

            Button sendModeBtn = filledBtn("我要发文件（选择要分享的文件）", cPrimaryPressed, cOnPrimary);
            sendModeBtn.setOnClickListener(v -> {
                dlg.dismiss();
                Intent i = new Intent(Intent.ACTION_GET_CONTENT);
                i.setType("*/*");
                i.addCategory(Intent.CATEGORY_OPENABLE);
                i.putExtra(Intent.EXTRA_ALLOW_MULTIPLE, true);
                startActivityForResult(i, PICK_TEMP_SEND);
            });
            LinearLayout.LayoutParams b1Lp = new LinearLayout.LayoutParams(MATCH, dp(46));
            b1Lp.bottomMargin = dp(10);
            sheet.addView(sendModeBtn, b1Lp);

            Button recvModeBtn = smallOutlineBtn("我要收文件（对方上传到本机）", v -> {
                dlg.dismiss();
                startTempShareAsync("recv", new JSONArray(), null);
            });
            sheet.addView(recvModeBtn, new LinearLayout.LayoutParams(MATCH, dp(46)));
        } else {
            final String shareUrl = activeStatus.optString("url", "");
            final String code = activeStatus.optString("code", "");
            final String mode = activeStatus.optString("mode", "recv");
            final int fileCount = activeStatus.optInt("file_count", 0);
            final int ttl = activeStatus.optInt("ttl_s", 600);
            final int upCount = activeStatus.optInt("uploaded_count", 0);
            final String lastFile = activeStatus.optString("last_file", "");

            TextView modeDesc = label(
                    "send".equals(mode)
                            ? ("当前模式：仅分享已选中的 " + fileCount + " 个文件")
                            : "当前模式：等待对方上传文件到本机",
                    12, cPrimary, true);
            LinearLayout.LayoutParams mdLp = new LinearLayout.LayoutParams(MATCH, WRAP);
            mdLp.topMargin = dp(4);
            mdLp.bottomMargin = dp(12);
            sheet.addView(modeDesc, mdLp);

            FrameLayout qrBox = new FrameLayout(this);
            qrBox.setBackground(roundBox(Color.WHITE, cBorder, 16));
            qrBox.setPadding(dp(12), dp(12), dp(12), dp(12));
            final ImageView qrImg = new ImageView(this);
            qrImg.setScaleType(ImageView.ScaleType.FIT_CENTER);
            qrBox.addView(qrImg, new FrameLayout.LayoutParams(MATCH, MATCH));
            LinearLayout.LayoutParams qbLp = new LinearLayout.LayoutParams(dp(208), dp(208));
            qbLp.gravity = Gravity.CENTER_HORIZONTAL;
            sheet.addView(qrBox, qbLp);

            ioPool.execute(() -> {
                try {
                    byte[] png = Mobile.generateQRPNG(shareUrl, 512);
                    final Bitmap bmp = BitmapFactory.decodeByteArray(png, 0, png.length);
                    ui.post(() -> {
                        if (dlg.isShowing() && bmp != null) {
                            qrImg.setImageBitmap(bmp);
                        }
                    });
                } catch (Exception ignored) {
                }
            });

            String spaced = code.length() == 6 ? (code.substring(0, 3) + " " + code.substring(3)) : code;
            TextView codeTv = label(spaced, 26, cOnSurface, true);
            codeTv.setTypeface(Typeface.MONOSPACE);
            codeTv.setGravity(Gravity.CENTER);
            LinearLayout.LayoutParams cLp = new LinearLayout.LayoutParams(MATCH, WRAP);
            cLp.topMargin = dp(12);
            sheet.addView(codeTv, cLp);

            tempTtlTv = label(String.format(Locale.US, "10 分钟有效 · 剩余 %02d:%02d", ttl / 60, ttl % 60), 12, cOnSurfaceVariant, false);
            tempTtlTv.setGravity(Gravity.CENTER);
            LinearLayout.LayoutParams tLp = new LinearLayout.LayoutParams(MATCH, WRAP);
            tLp.topMargin = dp(4);
            sheet.addView(tempTtlTv, tLp);

            TextView urlTv = label(shareUrl, 11, cOnSurfaceVariant, false);
            urlTv.setTypeface(Typeface.MONOSPACE);
            urlTv.setGravity(Gravity.CENTER);
            LinearLayout.LayoutParams uLp = new LinearLayout.LayoutParams(MATCH, WRAP);
            uLp.topMargin = dp(4);
            sheet.addView(urlTv, uLp);

            tempUploadTv = label("", 12, cPrimary, true);
            tempUploadTv.setGravity(Gravity.CENTER);
            tempUploadTv.setVisibility(upCount > 0 ? View.VISIBLE : View.GONE);
            if (upCount > 0) {
                tempUploadTv.setText("已收到 " + upCount + " 个文件" + (lastFile.isEmpty() ? "" : " · " + lastFile));
            }
            LinearLayout.LayoutParams upLp = new LinearLayout.LayoutParams(MATCH, WRAP);
            upLp.topMargin = dp(6);
            sheet.addView(tempUploadTv, upLp);

            tempTxBox = new LinearLayout(this);
            tempTxBox.setOrientation(LinearLayout.VERTICAL);
            tempTxBox.setPadding(dp(12), dp(10), dp(12), dp(10));
            tempTxBox.setBackground(roundBox(cSurfaceAlt, cPrimary, 12));
            tempTxBox.setVisibility(View.GONE);
            tempTxTitleTv = label("正在传输…", 12, cOnSurface, true);
            tempTxTitleTv.setSingleLine(true);
            tempTxTitleTv.setEllipsize(TextUtils.TruncateAt.MIDDLE);
            tempTxBox.addView(tempTxTitleTv, new LinearLayout.LayoutParams(MATCH, WRAP));
            tempTxBar = new ProgressBar(this, null, android.R.attr.progressBarStyleHorizontal);
            tempTxBar.setMax(1000);
            tempTxBar.setIndeterminate(true);
            tempTxBar.setProgressTintList(ColorStateList.valueOf(cPrimary));
            tempTxBar.setIndeterminateTintList(ColorStateList.valueOf(cPrimary));
            tempTxBar.setProgressBackgroundTintList(ColorStateList.valueOf(cBorder));
            LinearLayout.LayoutParams tpbLp = new LinearLayout.LayoutParams(MATCH, dp(5));
            tpbLp.topMargin = dp(6);
            tempTxBox.addView(tempTxBar, tpbLp);
            tempTxMetaTv = label("", 11, cOnSurfaceVariant, false);
            tempTxMetaTv.setSingleLine(false);
            tempTxMetaTv.setMaxLines(2);
            LinearLayout.LayoutParams tmLp = new LinearLayout.LayoutParams(MATCH, WRAP);
            tmLp.topMargin = dp(6);
            tempTxBox.addView(tempTxMetaTv, tmLp);
            LinearLayout.LayoutParams txbLp = new LinearLayout.LayoutParams(MATCH, WRAP);
            txbLp.topMargin = dp(10);
            sheet.addView(tempTxBox, txbLp);

            LinearLayout btnRow = new LinearLayout(this);
            btnRow.setOrientation(LinearLayout.HORIZONTAL);
            LinearLayout.LayoutParams brLp = new LinearLayout.LayoutParams(MATCH, WRAP);
            brLp.topMargin = dp(14);

            Button copyBtn = smallOutlineBtn("复制链接", v -> copyText("临时快传链接", shareUrl, "已复制临时快传链接"));
            LinearLayout.LayoutParams cpLp = new LinearLayout.LayoutParams(0, dp(42), 1f);
            cpLp.rightMargin = dp(8);
            btnRow.addView(copyBtn, cpLp);

            Button stopBtn = filledBtn("结束快传", cError, cOnError);
            stopBtn.setOnClickListener(v -> {
                dlg.dismiss();
                stopTempShareSession();
            });
            btnRow.addView(stopBtn, new LinearLayout.LayoutParams(0, dp(42), 1f));
            sheet.addView(btnRow, brLp);

            ui.removeCallbacks(tempPollTask);
            ui.postDelayed(tempPollTask, 2000);
            pollActiveTransferOnce();
        }

        dlg.setOnDismissListener(d -> {
            ui.removeCallbacks(tempPollTask);
            tempTtlTv = null;
            tempUploadTv = null;
            tempTxBox = null;
            tempTxTitleTv = null;
            tempTxBar = null;
            tempTxMetaTv = null;
        });
        dlg.setContentView(sheet);
        Window w = dlg.getWindow();
        if (w != null) {
            w.setBackgroundDrawable(new ColorDrawable(Color.TRANSPARENT));
            w.setLayout((int) (getResources().getDisplayMetrics().widthPixels * 0.92), WRAP);
        }
        dlg.show();
    }

    private void startTempSendFromUris(final List<Uri> uris) {
        toast("正在准备 " + uris.size() + " 个文件…");
        new Thread(() -> {
            File stageDir = new File(new File(getCacheDir(), "temp-send"), String.valueOf(System.nanoTime()));
            List<File> copied = new ArrayList<>();
            try {
                JSONArray arr = new JSONArray();
                for (int i = 0; i < uris.size(); i++) {
                    File f = copyToCache(uris.get(i), new File(stageDir, String.valueOf(i)));
                    copied.add(f);
                    arr.put(f.getAbsolutePath());
                }
                startTempShareSync("send", arr, stageDir);
            } catch (Exception e) {
                for (File f : copied) {
                    deleteTree(f, f.getParentFile());
                }
                stageDir.delete();
                ui.post(() -> toast("准备文件失败：" + e.getMessage()));
            }
        }).start();
    }

    private void startTempShareAsync(final String mode, final JSONArray paths, final File stageDir) {
        toast("正在创建临时快传…");
        new Thread(() -> startTempShareSync(mode, paths, stageDir)).start();
    }

    private void startTempShareSync(final String mode, final JSONArray paths, final File stageDir) {
        try {
            cleanupTempStageDir();
            tempSendStageDir = stageDir;
            String raw = Mobile.startTempShare(
                    TransferService.getWebBaseUrl(MainActivity.this),
                    keyFile().getAbsolutePath(),
                    deviceName,
                    mode,
                    paths.toString(),
                    inboxDir().getAbsolutePath(),
                    lanHint());
            final String url = new JSONObject(raw).optString("url", "");
            ui.post(() -> {
                webRunning = true;
                webShareUrl = url;
                startKeepAlive("临时快传中");
                refreshReceiveUi();
                openTempShareSheet();
            });
        } catch (Exception e) {
            Log.e(TAG, "startTempShare", e);
            cleanupTempStageDir();
            ui.post(() -> toast("创建临时快传失败：" + e.getMessage()));
        }
    }

    private void stopTempShareSession() {
        ui.removeCallbacks(tempPollTask);
        new Thread(() -> {
            try {
                Mobile.stopTempShare();
            } catch (Exception e) {
                Log.w(TAG, "stopTempShare", e);
            }
            cleanupTempStageDir();
            ui.post(() -> {
                webRunning = false;
                webShareUrl = "";
                if (!recvRunning) {
                    stopKeepAlive();
                }
                refreshReceiveUi();
                toast("已结束临时快传");
            });
        }).start();
    }

    private void cleanupTempStageDir() {
        File d = tempSendStageDir;
        tempSendStageDir = null;
        if (d != null && d.exists()) {
            deleteTree(d, d.getParentFile());
        }
    }

    private void openTempJoinSheet(final String shareUrl) {
        if (tempJoinDialog != null && tempJoinDialog.isShowing()) {
            tempJoinDialog.dismiss();
        }
        Dialog dlg = new Dialog(this);
        dlg.requestWindowFeature(Window.FEATURE_NO_TITLE);
        dlg.setCanceledOnTouchOutside(true);
        tempJoinDialog = dlg;

        LinearLayout sheet = new LinearLayout(this);
        sheet.setOrientation(LinearLayout.VERTICAL);
        sheet.setPadding(dp(20), dp(18), dp(20), dp(20));
        sheet.setBackground(roundBox(cSheetBg, cBorder, 20));

        LinearLayout topRow = new LinearLayout(this);
        topRow.setOrientation(LinearLayout.HORIZONTAL);
        topRow.setGravity(Gravity.CENTER_VERTICAL);
        topRow.addView(label("临时快传", 18, cOnSurface, true), new LinearLayout.LayoutParams(0, WRAP, 1f));
        TextView closeTv = label("关闭", 13, cOnSurfaceVariant, false);
        closeTv.setPadding(dp(8), dp(4), dp(8), dp(4));
        closeTv.setOnClickListener(v -> dlg.dismiss());
        topRow.addView(closeTv, new LinearLayout.LayoutParams(WRAP, WRAP));
        sheet.addView(topRow, new LinearLayout.LayoutParams(MATCH, WRAP));

        final TextView statusTv = label("正在连接对方会话…", 13, cOnSurfaceVariant, false);
        LinearLayout.LayoutParams stLp = new LinearLayout.LayoutParams(MATCH, WRAP);
        stLp.topMargin = dp(8);
        stLp.bottomMargin = dp(12);
        sheet.addView(statusTv, stLp);

        final LinearLayout bodyBox = new LinearLayout(this);
        bodyBox.setOrientation(LinearLayout.VERTICAL);
        sheet.addView(bodyBox, new LinearLayout.LayoutParams(MATCH, WRAP));

        tempJoinTxBox = new LinearLayout(this);
        tempJoinTxBox.setOrientation(LinearLayout.VERTICAL);
        tempJoinTxBox.setPadding(dp(12), dp(10), dp(12), dp(10));
        tempJoinTxBox.setBackground(roundBox(cSurfaceAlt, cPrimary, 12));
        tempJoinTxBox.setVisibility(View.GONE);
        tempJoinTxTitleTv = label("准备传输…", 12, cOnSurface, true);
        tempJoinTxTitleTv.setSingleLine(true);
        tempJoinTxTitleTv.setEllipsize(TextUtils.TruncateAt.MIDDLE);
        tempJoinTxBox.addView(tempJoinTxTitleTv, new LinearLayout.LayoutParams(MATCH, WRAP));
        tempJoinTxBar = new ProgressBar(this, null, android.R.attr.progressBarStyleHorizontal);
        tempJoinTxBar.setMax(1000);
        tempJoinTxBar.setIndeterminate(true);
        tempJoinTxBar.setProgressTintList(ColorStateList.valueOf(cPrimary));
        tempJoinTxBar.setIndeterminateTintList(ColorStateList.valueOf(cPrimary));
        tempJoinTxBar.setProgressBackgroundTintList(ColorStateList.valueOf(cBorder));
        LinearLayout.LayoutParams jpbLp = new LinearLayout.LayoutParams(MATCH, dp(5));
        jpbLp.topMargin = dp(6);
        tempJoinTxBox.addView(tempJoinTxBar, jpbLp);
        tempJoinTxMetaTv = label("", 11, cOnSurfaceVariant, false);
        tempJoinTxMetaTv.setSingleLine(false);
        tempJoinTxMetaTv.setMaxLines(2);
        LinearLayout.LayoutParams jmLp = new LinearLayout.LayoutParams(MATCH, WRAP);
        jmLp.topMargin = dp(6);
        tempJoinTxBox.addView(tempJoinTxMetaTv, jmLp);
        tempJoinActionRow = new LinearLayout(this);
        tempJoinActionRow.setOrientation(LinearLayout.HORIZONTAL);
        tempJoinActionRow.setVisibility(View.GONE);
        LinearLayout.LayoutParams jarLp = new LinearLayout.LayoutParams(MATCH, WRAP);
        jarLp.topMargin = dp(8);
        tempJoinPauseBtn = smallOutlineBtn("暂停", v -> toggleActiveTransferPause());
        LinearLayout.LayoutParams jprLp = new LinearLayout.LayoutParams(0, dp(32), 1f);
        jprLp.rightMargin = dp(8);
        tempJoinActionRow.addView(tempJoinPauseBtn, jprLp);
        tempJoinCancelBtn = smallOutlineBtn("取消", v -> cancelActiveTransfer());
        tempJoinCancelBtn.setTextColor(cError);
        tempJoinActionRow.addView(tempJoinCancelBtn, new LinearLayout.LayoutParams(0, dp(32), 1f));
        tempJoinTxBox.addView(tempJoinActionRow, jarLp);
        LinearLayout.LayoutParams jtxLp = new LinearLayout.LayoutParams(MATCH, WRAP);
        jtxLp.topMargin = dp(12);
        sheet.addView(tempJoinTxBox, jtxLp);

        dlg.setOnDismissListener(d -> {
            tempJoinTxBox = null;
            tempJoinTxTitleTv = null;
            tempJoinTxBar = null;
            tempJoinTxMetaTv = null;
            tempJoinActionRow = null;
            tempJoinPauseBtn = null;
            tempJoinCancelBtn = null;
            tempJoinActionBtn = null;
        });

        dlg.setContentView(sheet);
        Window w = dlg.getWindow();
        if (w != null) {
            w.setBackgroundDrawable(new ColorDrawable(Color.TRANSPARENT));
            w.setLayout((int) (getResources().getDisplayMetrics().widthPixels * 0.92), WRAP);
        }
        dlg.show();

        new Thread(() -> {
            try {
                String raw = Mobile.resolveTempShare(shareUrl);
                JSONObject o = new JSONObject(raw);
                final String peerName = o.optString("name", "对方设备");
                final String mode = o.optString("mode", "recv");
                final String via = o.optString("via", "relay");
                final JSONArray files = o.optJSONArray("files");
                final String viaLabel = "lan".equals(via) ? "局域网直连" : "云端中继";
                ui.post(() -> {
                    if (!dlg.isShowing()) {
                        return;
                    }
                    statusTv.setText("已连接：" + peerName + "（" + viaLabel + "）");
                    statusTv.setTextColor(cPrimary);
                    bodyBox.removeAllViews();

                    if ("send".equals(mode)) {
                        int count = files == null ? 0 : files.length();
                        TextView info = label("对方分享了 " + count + " 个文件：", 13, cOnSurface, true);
                        bodyBox.addView(info, new LinearLayout.LayoutParams(MATCH, WRAP));
                        if (files != null) {
                            int showMax = Math.min(count, 6);
                            for (int i = 0; i < showMax; i++) {
                                JSONObject f = files.optJSONObject(i);
                                if (f != null) {
                                    TextView item = label("• " + f.optString("name", ""), 12, cOnSurfaceVariant, false);
                                    LinearLayout.LayoutParams ilp = new LinearLayout.LayoutParams(MATCH, WRAP);
                                    ilp.topMargin = dp(4);
                                    bodyBox.addView(item, ilp);
                                }
                            }
                            if (count > showMax) {
                                bodyBox.addView(label("…等共 " + count + " 个文件", 12, cOnSurfaceVariant, false));
                            }
                        }
                        final Button dlBtn = filledBtn("全部下载到手机", cPrimaryPressed, cOnPrimary);
                        tempJoinActionBtn = dlBtn;
                        dlBtn.setEnabled(count > 0);
                        dlBtn.setOnClickListener(v -> {
                            dlBtn.setEnabled(false);
                            dlBtn.setText("正在下载…");
                            startKeepAlive("临时快传下载中");
                            startProgressPolling();
                            new Thread(() -> {
                                try {
                                    String res = Mobile.tempShareDownloadAll(shareUrl, inboxDir().getAbsolutePath());
                                    JSONObject rj = new JSONObject(res);
                                    JSONArray saved = rj.optJSONArray("files");
                                    int n = saved == null ? 0 : saved.length();
                                    pollReceivedOnce();
                                    ui.post(() -> {
                                        pollActiveTransferOnce();
                                        if (!recvRunning && !webRunning && !sendBusyFlag) {
                                            stopKeepAlive();
                                        }
                                        toast("已通过" + viaLabel + "下载 " + n + " 个文件");
                                        if (dlg.isShowing()) {
                                            dlg.dismiss();
                                        }
                                    });
                                } catch (Exception e) {
                                    ui.post(() -> {
                                        pollActiveTransferOnce();
                                        if (!recvRunning && !webRunning && !sendBusyFlag) {
                                            stopKeepAlive();
                                        }
                                        dlBtn.setEnabled(true);
                                        dlBtn.setText("全部下载到手机");
                                        String em = e.getMessage();
                                        if (em != null && em.contains("已取消")) {
                                            toast("已取消下载");
                                        } else {
                                            toast("下载失败：" + em);
                                        }
                                    });
                                }
                            }).start();
                        });
                        LinearLayout.LayoutParams dlp = new LinearLayout.LayoutParams(MATCH, dp(44));
                        dlp.topMargin = dp(14);
                        bodyBox.addView(dlBtn, dlp);
                    } else {
                        TextView info = label("对方正在等待接收文件，上传后不会保存设备配对记录。", 13, cOnSurfaceVariant, false);
                        bodyBox.addView(info, new LinearLayout.LayoutParams(MATCH, WRAP));
                        Button pickBtn = filledBtn("选择文件发送给对方", cPrimaryPressed, cOnPrimary);
                        tempJoinActionBtn = pickBtn;
                        pickBtn.setOnClickListener(v -> {
                            pendingTempJoinUrl = shareUrl;
                            Intent i = new Intent(Intent.ACTION_GET_CONTENT);
                            i.setType("*/*");
                            i.addCategory(Intent.CATEGORY_OPENABLE);
                            i.putExtra(Intent.EXTRA_ALLOW_MULTIPLE, true);
                            startActivityForResult(i, PICK_TEMP_UPLOAD);
                        });
                        LinearLayout.LayoutParams plp = new LinearLayout.LayoutParams(MATCH, dp(44));
                        plp.topMargin = dp(14);
                        bodyBox.addView(pickBtn, plp);
                    }
                });
            } catch (Exception e) {
                ui.post(() -> {
                    if (dlg.isShowing()) {
                        statusTv.setText("连接失败：" + e.getMessage());
                        statusTv.setTextColor(cError);
                    }
                });
            }
        }).start();
    }

    private void uploadTempJoinUris(final List<Uri> uris) {
        final String shareUrl = pendingTempJoinUrl;
        if (shareUrl == null || shareUrl.isEmpty()) {
            return;
        }
        toast("正在发送 " + uris.size() + " 个文件…");
        if (tempJoinActionBtn != null) {
            tempJoinActionBtn.setEnabled(false);
            tempJoinActionBtn.setText("正在上传…");
        }
        startKeepAlive("临时快传上传中");
        startProgressPolling();
        new Thread(() -> {
            File stageDir = new File(new File(getCacheDir(), "temp-upload"), String.valueOf(System.nanoTime()));
            List<File> copied = new ArrayList<>();
            try {
                JSONArray arr = new JSONArray();
                for (int i = 0; i < uris.size(); i++) {
                    File f = copyToCache(uris.get(i), new File(stageDir, String.valueOf(i)));
                    copied.add(f);
                    arr.put(f.getAbsolutePath());
                }
                String raw = Mobile.tempShareUploadFiles(shareUrl, arr.toString());
                JSONObject o = new JSONObject(raw);
                String via = o.optString("via", "relay");
                String viaLabel = "lan".equals(via) ? "局域网直连" : "云端中继";
                long bytes = o.optLong("bytes", 0L);
                long durMs = o.optLong("duration_ms", 0L);
                JSONArray upFiles = o.optJSONArray("files");
                int cnt = upFiles != null ? upFiles.length() : uris.size();
                String first = (upFiles != null && upFiles.length() > 0) ? upFiles.optString(0, "临时快传文件") : "临时快传文件";
                String recTitle = cnt > 1 ? first + " 等 " + cnt + " 个文件" : first;
                String spdSuffix = "";
                if (durMs >= 200L && bytes > 0L) {
                    spdSuffix = " · " + fmtSpeed((bytes * 1000L) / durMs);
                }
                final String doneMsg = "已通过" + viaLabel + "发送 " + cnt + " 个文件" + spdSuffix;
                ui.post(() -> {
                    pollActiveTransferOnce();
                    if (!recvRunning && !webRunning && !sendBusyFlag) {
                        stopKeepAlive();
                    }
                    addRecentEntry(new ReceivedEntry(recTitle, "", "", via, bytes,
                            System.currentTimeMillis(), "send", "临时快传"));
                    toast(doneMsg);
                    if (tempJoinDialog != null && tempJoinDialog.isShowing()) {
                        tempJoinDialog.dismiss();
                    }
                });
            } catch (Exception e) {
                ui.post(() -> {
                    pollActiveTransferOnce();
                    if (!recvRunning && !webRunning && !sendBusyFlag) {
                        stopKeepAlive();
                    }
                    if (tempJoinActionBtn != null) {
                        tempJoinActionBtn.setEnabled(true);
                        tempJoinActionBtn.setText("选择文件发送给对方");
                    }
                    String em = e.getMessage();
                    if (em != null && em.contains("已取消")) {
                        toast("已取消发送");
                    } else {
                        toast("发送失败：" + em);
                    }
                });
            } finally {
                for (File f : copied) {
                    deleteTree(f, f.getParentFile());
                }
                stageDir.delete();
            }
        }).start();
    }

    private void pollConnectionStatusOnce() {
        ioPool.execute(() -> {
            try {
                String raw = Mobile.connectionStatus();
                ui.post(() -> applyConnectionStatusJson(raw));
            } catch (Exception ignored) {
            }
        });
    }

    private void applyConnectionStatusJson(String raw) {
        if (raw == null || raw.trim().isEmpty()) {
            return;
        }
        try {
            JSONObject o = new JSONObject(raw);
            final String st = o.optString("signal_state", "offline");
            final Set<String> nextCloud = new HashSet<>();
            JSONArray cp = o.optJSONArray("online_peers");
            if (cp != null) {
                for (int i = 0; i < cp.length(); i++) {
                    String id = cp.optString(i, "");
                    if (!id.isEmpty()) {
                        nextCloud.add(id);
                    }
                }
            }
            final Set<String> nextLan = new HashSet<>();
            JSONObject lp = o.optJSONObject("lan_peers");
            if (lp != null) {
                Iterator<String> it = lp.keys();
                while (it.hasNext()) {
                    String id = it.next();
                    if (!id.isEmpty() && !lp.optString(id, "").isEmpty()) {
                        nextLan.add(id);
                    }
                }
            }
            boolean changed = !st.equals(signalState)
                    || !nextCloud.equals(cloudOnlineSet)
                    || !nextLan.equals(lanOnlineSet);
            if (!changed) {
                return;
            }
            signalState = st;
            cloudOnlineSet.clear();
            cloudOnlineSet.addAll(nextCloud);
            lanOnlineSet.clear();
            lanOnlineSet.addAll(nextLan);
            refreshReceiveUi();
            if (activePage == 0) {
                renderPeerSection();
            } else if (activePage == 2) {
                renderDevicesPage();
            }
            if (shareDialog != null && shareDialog.isShowing()) {
                refreshShareSheetPeers();
            }
        } catch (Exception ignored) {
        }
    }

    private void refreshTargetStatusUi() {
        String stText = "离线";
        int dotClr = Color.parseColor("#99FFFFFF");
        boolean online = false;
        if (selectedTarget == null || selectedTarget.isEmpty()) {
            stText = "未选择";
        } else if (!isAutoTarget(selectedTarget) || lanOnlineSet.contains(selectedTarget)) {
            stText = "局域网高速直连";
            dotClr = Color.parseColor("#6EE7B7");
            online = true;
        } else if ("online".equals(signalState) && cloudOnlineSet.contains(selectedTarget)) {
            stText = "云端在线";
            dotClr = Color.parseColor("#A7F3D0");
            online = true;
        } else if ("connecting".equals(signalState)) {
            stText = "重连中…";
            dotClr = cWarn;
        }
        if (sendTargetTitle != null) {
            sendTargetTitle.setText("直发给 " + resolveTargetName(selectedTarget) + " · " + stText);
        }
        if (sendRecvStatusBtn != null) {
            sendRecvStatusBtn.setText(stText);
        }
        if (recvPulseDot != null) {
            recvPulseDot.setBackground(oval(dotClr));
        }
        if (online) {
            startDotPulse();
        } else {
            stopDotPulse();
        }
    }

    private void refreshReceiveUi() {
        refreshTargetStatusUi();
        if (homeSignalBar != null && homeSigDot != null && homeSigText != null && homeSigHost != null) {
            if (recvRunning && "online".equals(signalState)) {
                homeSignalBar.setVisibility(View.GONE);
            } else {
                String barText = "后台接收已关闭，点此开启";
                int barBg = cCard;
                int barBorder = cBorder;
                int barFg = cOnSurfaceVariant;
                int dotClr = cOutline;
                String hostText = "开启";
                if (recvRunning) {
                    if ("connecting".equals(signalState)) {
                        barText = "正在连接云端信令，同 Wi-Fi 仍可直传";
                        barBg = cWarnContainer;
                        barBorder = cWarn;
                        barFg = cWarn;
                        dotClr = cWarn;
                        hostText = "重连";
                    } else {
                        barText = "云端未连接 · 同 Wi-Fi 仍可直传";
                        barBg = cWarnContainer;
                        barBorder = cWarn;
                        barFg = cWarn;
                        dotClr = cWarn;
                        hostText = "重试";
                    }
                }
                homeSignalBar.setBackground(ripple(roundBox(barBg, barBorder, 12), 12));
                homeSigDot.setBackground(oval(dotClr));
                homeSigText.setText(barText);
                homeSigText.setTextColor(barFg);
                homeSigHost.setText(hostText);
                homeSigHost.setTextColor(barFg);
                homeSignalBar.setVisibility(View.VISIBLE);
            }
        }
        if (setRecvBtn != null) {
            setRecvBtn.setText(recvRunning ? "已开启" : "已关闭");
            setRecvBtn.setBackground(ripple(roundBox(
                    recvRunning ? cPrimaryContainer : cSurface,
                    recvRunning ? cPrimary : cBorder, 10), 10));
            setRecvBtn.setTextColor(recvRunning ? cOnPrimaryContainer : cOnSurfaceVariant);
        }
    }

    private void refreshClipboardPreviewBar() {
        if (homeClipBar == null || homeClipText == null) {
            return;
        }
        try {
            ClipboardManager cm = (ClipboardManager) getSystemService(CLIPBOARD_SERVICE);
            if (cm == null || !cm.hasPrimaryClip() || cm.getPrimaryClip() == null
                    || cm.getPrimaryClip().getItemCount() == 0) {
                homeClipBar.setVisibility(View.GONE);
                return;
            }
            CharSequence cs = cm.getPrimaryClip().getItemAt(0).coerceToText(this);
            String raw = cs == null ? "" : cs.toString().trim().replaceAll("\\s+", " ");
            if (raw.isEmpty()) {
                homeClipBar.setVisibility(View.GONE);
                return;
            }
            String snip = raw.length() > 18 ? raw.substring(0, 17) + "…" : raw;
            homeClipText.setText("检测到剪贴板：" + snip);
            homeClipBar.setVisibility(View.VISIBLE);
        } catch (Throwable ignored) {
            homeClipBar.setVisibility(View.GONE);
        }
    }

    private static String extractPort(String addr) {
        if (addr == null) {
            return "";
        }
        int i = addr.lastIndexOf(':');
        return i >= 0 ? addr.substring(i + 1).trim() : "";
    }

    // ================= Receive Poll -> Public Download or Custom Directory =================

    private void pollReceivedOnce() {
        if (TransferService.isRecvRunning()) {
            Intent i = new Intent(this, TransferService.class).setAction(TransferService.ACTION_SWEEP_INBOX);
            startService(i);
            return;
        }
        sweepLeftoverInbox();
    }

    private void sweepLeftoverInbox() {
        ioPool.execute(() -> {
            try {
                String raw = Mobile.pollReceived();
                if (raw != null && !raw.equals("[]") && !raw.trim().isEmpty()) {
                    JSONArray arr = new JSONArray(raw);
                    for (int i = 0; i < arr.length(); i++) {
                        JSONObject o = arr.getJSONObject(i);
                        String fileName = o.optString("file", "");
                        String fullPath = o.optString("full_path", "");
                        if (!fileName.isEmpty() && !fullPath.isEmpty()) {
                            File src = new File(fullPath);
                            if (src.isFile()) {
                                exportStagedFile(src, fileName, o.optString("via", ""), o.optString("peer", ""),
                                        o.optLong("bytes", 0), o.optLong("ts", System.currentTimeMillis()));
                            }
                        }
                    }
                }
            } catch (Exception e) {
                Log.w(TAG, "sweepPoll", e);
            }
            File[] files = inboxDir().listFiles();
            if (files == null) {
                return;
            }
            for (File f : files) {
                if (f.isFile() && !f.getName().endsWith(".ftpart")) {
                    exportStagedFile(f, f.getName(), "", "", f.length(), f.lastModified());
                }
            }
        });
    }

    private void exportStagedFile(File src, String fileName, String via, String peer, long bytes, long ts) {
        String[] saved = TransferService.exportStagedFile(this, src, fileName);
        if (saved == null) {
            return;
        }
        final String savedLabel = saved[1];
        final ReceivedEntry entry = new ReceivedEntry(fileName, saved[0], TransferService.guessMime(fileName), via, bytes, ts, "recv", peer);
        ui.post(() -> {
            addRecentEntry(entry);
            toast("已保存到「" + savedLabel + "」：" + fileName);
        });
    }

    private String saveToCustomTree(Uri treeUri, File src, String fileName, String mime) {
        Uri docUri = null;
        try {
            String treeDocId = DocumentsContract.getTreeDocumentId(treeUri);
            Uri parentDocUri = DocumentsContract.buildDocumentUriUsingTree(treeUri, treeDocId);
            docUri = DocumentsContract.createDocument(getContentResolver(), parentDocUri, mime, fileName);
            if (docUri == null) {
                return null;
            }
            try (InputStream in = new FileInputStream(src);
                 OutputStream out = getContentResolver().openOutputStream(docUri, "w")) {
                if (out == null) {
                    throw new Exception("openOutputStream null");
                }
                copyStream(in, out);
            }
            return docUri.toString();
        } catch (Exception e) {
            Log.e(TAG, "saveToCustomTree", e);
            if (docUri != null) {
                try {
                    DocumentsContract.deleteDocument(getContentResolver(), docUri);
                } catch (Exception ignored) {
                }
            }
            return null;
        }
    }

    private String saveToPublicDownloads(File src, String fileName, String mime) {
        if (Build.VERSION.SDK_INT >= 29) {
            ContentValues cv = new ContentValues();
            cv.put(MediaStore.Downloads.DISPLAY_NAME, fileName);
            cv.put(MediaStore.Downloads.MIME_TYPE, mime);
            cv.put(MediaStore.Downloads.RELATIVE_PATH, Environment.DIRECTORY_DOWNLOADS);
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
            File dir = Environment.getExternalStoragePublicDirectory(Environment.DIRECTORY_DOWNLOADS);
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

    private void loadDownloadDirPrefs() {
        SharedPreferences sp = getPreferences(MODE_PRIVATE);
        String rawUri = sp.getString("customDownloadTreeUri", "").trim();
        String rawLbl = sp.getString("customDownloadLabel", "").trim();
        if (!rawUri.isEmpty()) {
            try {
                Uri parsed = Uri.parse(rawUri);
                if (hasPersistedTreePermission(parsed)) {
                    customDownloadTreeUri = rawUri;
                    customDownloadLabel = rawLbl.isEmpty() ? formatTreeUriLabel(parsed) : rawLbl;
                    return;
                }
            } catch (Exception e) {
                Log.w(TAG, "loadDownloadDirPrefs", e);
            }
            sp.edit().remove("customDownloadTreeUri").remove("customDownloadLabel").apply();
        }
        customDownloadTreeUri = "";
        customDownloadLabel = "";
    }

    private boolean hasPersistedTreePermission(Uri uri) {
        for (android.content.UriPermission p : getContentResolver().getPersistedUriPermissions()) {
            if (p.getUri().equals(uri) && p.isWritePermission()) {
                return true;
            }
        }
        return false;
    }

    private void pickDownloadDirectory() {
        try {
            Intent i = new Intent(Intent.ACTION_OPEN_DOCUMENT_TREE);
            i.addFlags(Intent.FLAG_GRANT_READ_URI_PERMISSION
                    | Intent.FLAG_GRANT_WRITE_URI_PERMISSION
                    | Intent.FLAG_GRANT_PERSISTABLE_URI_PERMISSION
                    | Intent.FLAG_GRANT_PREFIX_URI_PERMISSION);
            startActivityForResult(i, PICK_DOWNLOAD_DIR);
        } catch (Exception e) {
            toast("无法打开文件夹选择器：" + e.getMessage());
        }
    }

    private void resetDownloadDirectory() {
        customDownloadTreeUri = "";
        customDownloadLabel = "";
        getPreferences(MODE_PRIVATE).edit()
                .remove("customDownloadTreeUri")
                .remove("customDownloadLabel")
                .apply();
        refreshDownloadDirUi();
        toast("已恢复默认系统 Download 文件夹");
    }

    private void refreshDownloadDirUi() {
        boolean custom = customDownloadTreeUri != null && !customDownloadTreeUri.isEmpty();
        if (setDownloadDirSub != null) {
            setDownloadDirSub.setText(custom ? customDownloadLabel : "默认：系统 Download 文件夹");
        }
        if (setDownloadResetBtn != null) {
            setDownloadResetBtn.setVisibility(custom ? View.VISIBLE : View.GONE);
        }
        if (recentEmptySub != null) {
            recentEmptySub.setText("收到的文件会自动放进" + currentDownloadDisplayName() + "。");
        }
    }

    private String currentDownloadDisplayName() {
        if (customDownloadTreeUri != null && !customDownloadTreeUri.isEmpty() && !customDownloadLabel.isEmpty()) {
            return "「" + customDownloadLabel + "」";
        }
        return "系统 Download 文件夹";
    }

    private String currentDownloadShortLabel() {
        if (customDownloadTreeUri != null && !customDownloadTreeUri.isEmpty() && !customDownloadLabel.isEmpty()) {
            return customDownloadLabel;
        }
        return "下载";
    }

    private static String formatTreeUriLabel(Uri treeUri) {
        try {
            String docId = DocumentsContract.getTreeDocumentId(treeUri);
            if (docId != null && !docId.isEmpty()) {
                if (docId.startsWith("primary:")) {
                    String rel = docId.substring("primary:".length());
                    return rel.isEmpty() ? "内部存储根目录" : "内部存储 / " + rel;
                }
                int colon = docId.indexOf(':');
                if (colon > 0) {
                    String vol = docId.substring(0, colon);
                    String rel = docId.substring(colon + 1);
                    return rel.isEmpty() ? vol : vol + " / " + rel;
                }
                return docId;
            }
        } catch (Exception ignored) {
        }
        return "自定义文件夹";
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
        final JSONObject rec = TransferService.makeRecentJson(
                entry.name, entry.uri, entry.mime, entry.via, entry.bytes, entry.ts,
                entry.dir, entry.peer, entry.status, entry.error);
        ioPool.execute(() -> TransferService.appendRecentRecord(this, rec));
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
                            o.optString("peer", ""),
                            o.optString("status", ""),
                            o.optString("error", "")));
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
                    if (!r.status.isEmpty()) {
                        o.put("status", r.status);
                    }
                    if (!r.error.isEmpty()) {
                        o.put("error", r.error);
                    }
                    arr.put(o);
                }
                TransferService.saveRecentList(this, arr);
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
            tv.setTextColor(sel ? cOnPrimaryContainer : cOnSurfaceVariant);
            tv.setBackground(roundBox(sel ? cPrimaryContainer : Color.TRANSPARENT, sel ? 0 : cBorder, 99));
        }
    }

    private void updateHomeLatestCard() {
        if (homeLatestCard == null || homeLatestTitle == null || homeLatestSub == null || homeLatestBtn == null) {
            return;
        }
        if (recentEntries.isEmpty()) {
            homeLatestCard.setVisibility(View.GONE);
            return;
        }
        final ReceivedEntry latest = recentEntries.get(0);
        final boolean isSend = "send".equals(latest.dir);
        final boolean isCancelled = "cancelled".equals(latest.status) || "peer_cancelled".equals(latest.status);
        final boolean isFailed = "failed".equals(latest.status);

        String verb = isSend ? "刚发出" : "刚收到";
        homeLatestTitle.setText(verb + " · " + latest.name);
        String peerShow = displayPeerLabel(latest.peer);
        String sizePart = latest.bytes > 0 ? fmtBytes(latest.bytes) : "";
        String sub = peerShow;
        if (!sizePart.isEmpty()) {
            sub = peerShow.isEmpty() ? sizePart : (sizePart + " · " + peerShow);
        }
        homeLatestSub.setText(sub);

        if (isFailed || isCancelled) {
            homeLatestBtn.setText("记录");
            homeLatestBtn.setOnClickListener(v -> showPage(1));
        } else if (!isSend && latest.uri != null && !latest.uri.isEmpty()) {
            homeLatestBtn.setText("打开");
            homeLatestBtn.setOnClickListener(v -> openReceivedEntry(latest));
        } else {
            homeLatestBtn.setText("查看");
            homeLatestBtn.setOnClickListener(v -> showPage(1));
        }
        homeLatestCard.setVisibility(View.VISIBLE);
    }

    private void renderRecentList() {
        updateHomeLatestCard();
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
            final boolean isCancelledSt = "cancelled".equals(r.status) || "peer_cancelled".equals(r.status);
            final boolean isFailedSt = "failed".equals(r.status);
            final boolean isSpecialOutcome = isCancelledSt || isFailedSt;

            LinearLayout row = new LinearLayout(this);
            row.setOrientation(LinearLayout.HORIZONTAL);
            row.setGravity(Gravity.CENTER_VERTICAL);
            row.setPadding(dp(12), dp(12), dp(12), dp(12));
            row.setBackground(ripple(roundBox(Color.TRANSPARENT, 0, 12), 12));
            if (isSpecialOutcome) {
                final String clickMsg;
                if ("peer_cancelled".equals(r.status)) {
                    clickMsg = "对方已取消：" + r.name;
                } else if ("cancelled".equals(r.status)) {
                    clickMsg = "已取消：" + r.name;
                } else {
                    clickMsg = !r.error.isEmpty() ? ("传输失败：" + r.error) : ("传输失败：" + r.name);
                }
                row.setOnClickListener(v -> toast(clickMsg));
            } else if (!isSend && r.uri != null && !r.uri.isEmpty()) {
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
                    ? (r.peer == null || r.peer.isEmpty() ? "已发出" : "发往 " + displayPeerLabel(r.peer))
                    : (r.peer == null || r.peer.isEmpty() ? "已收到" : "来自 " + displayPeerLabel(r.peer));
            String sizePart = r.bytes > 0 ? (" · " + fmtBytes(r.bytes)) : "";
            String timeStr = r.ts > 0 ? " · " + sdf.format(new Date(r.ts)) : "";

            LinearLayout metaRow = new LinearLayout(this);
            metaRow.setOrientation(LinearLayout.HORIZONTAL);
            metaRow.setGravity(Gravity.CENTER_VERTICAL);
            String badgeKey = isSpecialOutcome ? r.status : (r.via != null ? r.via.trim() : "");
            if (!badgeKey.isEmpty()) {
                TextView chanBadge = label("", 10, cOnSurface, true);
                chanBadge.setPadding(dp(6), dp(2), dp(6), dp(2));
                stylePathBadge(chanBadge, badgeKey);
                LinearLayout.LayoutParams bLp = new LinearLayout.LayoutParams(WRAP, WRAP);
                bLp.rightMargin = dp(6);
                metaRow.addView(chanBadge, bLp);
            }
            TextView mt = label(dirPrefix + sizePart + timeStr, 12, cOnSurfaceVariant, false);
            mt.setSingleLine(true);
            mt.setEllipsize(TextUtils.TruncateAt.END);
            metaRow.addView(mt, new LinearLayout.LayoutParams(0, WRAP, 1f));

            LinearLayout.LayoutParams mtLp = new LinearLayout.LayoutParams(MATCH, WRAP);
            mtLp.topMargin = dp(3);
            col.addView(metaRow, mtLp);
            row.addView(col, new LinearLayout.LayoutParams(0, WRAP, 1f));

            String hintText;
            int hintColor;
            if ("peer_cancelled".equals(r.status)) {
                hintText = "对方已取消";
                hintColor = cWarn;
            } else if ("cancelled".equals(r.status)) {
                hintText = "已取消";
                hintColor = cWarn;
            } else if ("failed".equals(r.status)) {
                hintText = "失败";
                hintColor = cError;
            } else {
                hintText = isSend ? "已发" : "打开";
                hintColor = isSend ? cOnSurfaceVariant : cPrimary;
            }
            TextView actionHint = label(hintText, 12, hintColor, false);
            LinearLayout.LayoutParams ahLp = new LinearLayout.LayoutParams(WRAP, WRAP);
            ahLp.leftMargin = dp(12);
            row.addView(actionHint, ahLp);

            recentListView.addView(row, new LinearLayout.LayoutParams(MATCH, WRAP));
            if (i < visible.size() - 1) {
                LinearLayout.LayoutParams divLp = new LinearLayout.LayoutParams(MATCH, 1);
                divLp.leftMargin = dp(12);
                divLp.rightMargin = dp(12);
                recentListView.addView(hairline(), divLp);
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
            toast("文件已保存在「" + currentDownloadShortLabel() + " / " + r.name + "」");
        }
    }

    // ================= Send Flows (Files, Text & Clipboard) =================

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
                Uri u = intent.getClipData().getItemAt(i).getUri();
                if (u != null) {
                    uris.add(u);
                }
            }
        } else if (intent.getParcelableExtra(Intent.EXTRA_STREAM) instanceof Uri) {
            uris.add(intent.getParcelableExtra(Intent.EXTRA_STREAM));
        }
        if (uris.isEmpty()) {
            if (Intent.ACTION_SEND.equals(action)) {
                String sharedText = intent.getStringExtra(Intent.EXTRA_TEXT);
                if (sharedText != null && !sharedText.trim().isEmpty()) {
                    showPage(0);
                    final String textToSend = sharedText;
                    ui.post(() -> showSendTextDialog(textToSend));
                }
            }
            return;
        }
        sharedUris = uris;
        showPage(0);
        final List<Uri> incoming = new ArrayList<>(uris);
        ui.post(() -> openShareSheet(incoming));
    }

    private void openShareSheet(final List<Uri> uris) {
        if (uris == null || uris.isEmpty()) {
            return;
        }
        if (shareDialog != null && shareDialog.isShowing()) {
            shareDialog.dismiss();
            shareDialog = null;
        }
        shareDialogUris = new ArrayList<>(uris);
        shareSelectedTarget = selectedTarget;

        Dialog dlg = new Dialog(this, android.R.style.Theme_Translucent_NoTitleBar);
        dlg.requestWindowFeature(Window.FEATURE_NO_TITLE);

        FrameLayout overlay = new FrameLayout(this);
        overlay.setBackgroundColor(Color.parseColor("#73000000"));
        overlay.setPadding(dp(16), dp(28), dp(16), dp(24));
        overlay.setOnClickListener(v -> dismissShareSheet(true));

        ScrollView scroller = new ScrollView(this);
        scroller.setFillViewport(false);
        FrameLayout.LayoutParams scLp = new FrameLayout.LayoutParams(MATCH, WRAP, Gravity.BOTTOM | Gravity.CENTER_HORIZONTAL);
        scLp.width = Math.min(getResources().getDisplayMetrics().widthPixels - dp(32), dp(392));

        LinearLayout sheet = new LinearLayout(this);
        sheet.setOrientation(LinearLayout.VERTICAL);
        sheet.setPadding(dp(20), dp(18), dp(20), dp(20));
        sheet.setBackground(roundBox(cSheetBg, cBorder, 24));
        sheet.setClickable(true);

        // Header row: "外部分享" pill + title + close X
        LinearLayout hdr = new LinearLayout(this);
        hdr.setOrientation(LinearLayout.HORIZONTAL);
        hdr.setGravity(Gravity.CENTER_VERTICAL);

        TextView pill = label("外部分享", 11, cOnPrimaryContainer, true);
        pill.setPadding(dp(9), dp(3), dp(9), dp(3));
        pill.setBackground(roundBox(cPrimaryContainer, 0, 99));
        LinearLayout.LayoutParams pLp = new LinearLayout.LayoutParams(WRAP, WRAP);
        pLp.rightMargin = dp(8);
        hdr.addView(pill, pLp);

        TextView title = label("发送到其他设备", 17, cOnSurface, true);
        hdr.addView(title, new LinearLayout.LayoutParams(0, WRAP, 1f));

        TextView closeX = label("✕", 14, cOnSurfaceVariant, true);
        closeX.setGravity(Gravity.CENTER);
        closeX.setBackground(ripple(oval(Color.TRANSPARENT), 99));
        closeX.setOnClickListener(v -> dismissShareSheet(true));
        hdr.addView(closeX, new LinearLayout.LayoutParams(dp(30), dp(30)));
        sheet.addView(hdr, new LinearLayout.LayoutParams(MATCH, WRAP));

        // Shared files preview card
        LinearLayout fileBox = new LinearLayout(this);
        fileBox.setOrientation(LinearLayout.VERTICAL);
        fileBox.setPadding(dp(14), dp(12), dp(14), dp(12));
        fileBox.setBackground(roundBox(cSurfaceAlt, cBorder, 14));

        long totalKnown = 0;
        boolean allKnown = true;
        int previewLimit = Math.min(3, uris.size());
        List<String> previewNames = new ArrayList<>(previewLimit);
        List<Long> previewSizes = new ArrayList<>(previewLimit);
        for (int i = 0; i < uris.size(); i++) {
            long sz = displaySize(uris.get(i));
            if (sz >= 0) {
                totalKnown += sz;
            } else {
                allKnown = false;
            }
            if (i < previewLimit) {
                previewNames.add(displayName(uris.get(i)));
                previewSizes.add(sz);
            }
        }
        String summaryStr = "待发送 " + uris.size() + " 个文件";
        if (totalKnown > 0) {
            summaryStr += (allKnown ? " · 共 " : " · 约 ") + fmtBytes(totalKnown);
        }
        TextView sumTv = label(summaryStr, 12, cOnSurface, true);
        LinearLayout.LayoutParams sumLp = new LinearLayout.LayoutParams(MATCH, WRAP);
        sumLp.bottomMargin = dp(6);
        fileBox.addView(sumTv, sumLp);

        for (int i = 0; i < previewNames.size(); i++) {
            LinearLayout row = new LinearLayout(this);
            row.setOrientation(LinearLayout.HORIZONTAL);
            row.setGravity(Gravity.CENTER_VERTICAL);
            TextView fn = label(previewNames.get(i), 12, cOnSurfaceVariant, false);
            fn.setSingleLine(true);
            fn.setEllipsize(TextUtils.TruncateAt.MIDDLE);
            row.addView(fn, new LinearLayout.LayoutParams(0, WRAP, 1f));
            long sz = previewSizes.get(i);
            if (sz >= 0) {
                TextView szTv = label(fmtBytes(sz), 11, cOnSurfaceVariant, false);
                LinearLayout.LayoutParams szLp = new LinearLayout.LayoutParams(WRAP, WRAP);
                szLp.leftMargin = dp(8);
                row.addView(szTv, szLp);
            }
            LinearLayout.LayoutParams rLp = new LinearLayout.LayoutParams(MATCH, WRAP);
            rLp.topMargin = dp(3);
            fileBox.addView(row, rLp);
        }
        if (uris.size() > previewLimit) {
            TextView moreTv = label("…还有 " + (uris.size() - previewLimit) + " 个文件", 11, cOnSurfaceVariant, false);
            LinearLayout.LayoutParams mLp = new LinearLayout.LayoutParams(MATCH, WRAP);
            mLp.topMargin = dp(4);
            fileBox.addView(moreTv, mLp);
        }

        LinearLayout.LayoutParams fbLp = new LinearLayout.LayoutParams(MATCH, WRAP);
        fbLp.topMargin = dp(14);
        sheet.addView(fileBox, fbLp);

        TextView devLbl = label("选择接收设备", 12, cOnSurfaceVariant, true);
        LinearLayout.LayoutParams dlLp = new LinearLayout.LayoutParams(MATCH, WRAP);
        dlLp.topMargin = dp(14);
        dlLp.bottomMargin = dp(6);
        sheet.addView(devLbl, dlLp);

        sharePeersBox = new LinearLayout(this);
        sharePeersBox.setOrientation(LinearLayout.VERTICAL);
        sheet.addView(sharePeersBox, new LinearLayout.LayoutParams(MATCH, WRAP));

        shareSendBtn = filledBtn("立即发送", cPrimaryPressed, cOnPrimary);
        shareSendBtn.setOnClickListener(v -> {
            List<PeerEntry> all = combinedPeers();
            if (all.isEmpty()) {
                List<Uri> keep = shareDialogUris;
                dismissShareSheet(false);
                sharedUris = keep;
                if (shareBannerView != null && keep != null && !keep.isEmpty()) {
                    shareBannerView.setText("已从其他应用带入 " + keep.size() + " 个文件 · 配对完成后点「发文件」发送");
                    shareBannerView.setVisibility(View.VISIBLE);
                }
                openPairSheet(0);
                return;
            }
            String tgt = shareSelectedTarget;
            if (!isAutoTarget(tgt) && !isLanTarget(tgt)) {
                toast("请先选择接收设备");
                return;
            }
            selectedTarget = tgt;
            saveLastPeer(tgt);
            renderPeerSection();
            String tgtName = resolveTargetName(tgt);
            List<Uri> toSend = shareDialogUris;
            dismissShareSheet(true);
            if (toSend != null && !toSend.isEmpty()) {
                sendUriBatch(toSend, deviceName, tgt, tgtName);
            }
        });
        LinearLayout.LayoutParams sbLp = new LinearLayout.LayoutParams(MATCH, dp(44));
        sbLp.topMargin = dp(14);
        sheet.addView(shareSendBtn, sbLp);

        Button cancelBtn = smallOutlineBtn("取消分享", v -> dismissShareSheet(true));
        LinearLayout.LayoutParams cbLp = new LinearLayout.LayoutParams(MATCH, dp(38));
        cbLp.topMargin = dp(8);
        sheet.addView(cancelBtn, cbLp);

        scroller.addView(sheet, new FrameLayout.LayoutParams(MATCH, WRAP));
        overlay.addView(scroller, scLp);

        dlg.setContentView(overlay);
        dlg.setCanceledOnTouchOutside(true);
        dlg.setOnCancelListener(d -> clearSharedPending());
        dlg.setOnDismissListener(d -> {
            if (shareDialog == dlg) {
                shareDialog = null;
                sharePeersBox = null;
                shareSendBtn = null;
                shareDialogUris = null;
            }
        });

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

        shareDialog = dlg;
        refreshShareSheetPeers();
        dlg.show();
    }

    private List<PeerEntry> combinedPeers() {
        List<PeerEntry> combined = new ArrayList<>();
        combined.addAll(pairedEntries);
        combined.addAll(lanEntries);
        return combined;
    }

    private void refreshShareSheetPeers() {
        if (sharePeersBox == null || shareSendBtn == null) {
            return;
        }
        sharePeersBox.removeAllViews();
        List<PeerEntry> combined = combinedPeers();
        if (combined.isEmpty()) {
            TextView empty = label("暂无已配对或局域网设备，请先配对。", 12, cOnSurfaceVariant, false);
            empty.setPadding(dp(12), dp(12), dp(12), dp(12));
            empty.setBackground(roundBox(cSurfaceAlt, cBorder, 12));
            sharePeersBox.addView(empty, new LinearLayout.LayoutParams(MATCH, WRAP));
            shareSendBtn.setText("去扫码配对设备");
            return;
        }

        boolean foundSel = false;
        for (PeerEntry e : combined) {
            if (e.value.equals(shareSelectedTarget)) {
                foundSel = true;
                break;
            }
        }
        if (!foundSel) {
            shareSelectedTarget = combined.get(0).value;
        }

        for (int i = 0; i < combined.size(); i++) {
            final PeerEntry entry = combined.get(i);
            final boolean sel = entry.value.equals(shareSelectedTarget);

            LinearLayout row = new LinearLayout(this);
            row.setOrientation(LinearLayout.HORIZONTAL);
            row.setGravity(Gravity.CENTER_VERTICAL);
            row.setPadding(dp(12), dp(10), dp(12), dp(10));
            row.setBackground(ripple(roundBox(
                    sel ? cPrimaryContainer : cCard,
                    sel ? cPrimary : cBorder, 12), 12));
            row.setOnClickListener(v -> {
                shareSelectedTarget = entry.value;
                refreshShareSheetPeers();
            });

            TextView nm = label(entry.name, 13, sel ? cOnPrimaryContainer : cOnSurface, true);
            nm.setSingleLine(true);
            nm.setEllipsize(TextUtils.TruncateAt.END);
            row.addView(nm, new LinearLayout.LayoutParams(0, WRAP, 1f));

            if (isAutoTarget(entry.value)) {
                String stLabel = "离线";
                int stBg = cSurfaceAlt;
                int stFg = cOnSurfaceVariant;
                if (lanOnlineSet.contains(entry.value)) {
                    stLabel = "局域网";
                    stBg = cPrimaryContainer;
                    stFg = cOnPrimaryContainer;
                } else if ("online".equals(signalState) && cloudOnlineSet.contains(entry.value)) {
                    stLabel = "云端在线";
                    stBg = cPrimaryContainer;
                    stFg = cOnPrimaryContainer;
                }
                TextView stBadge = label(stLabel, 10, stFg, true);
                stBadge.setPadding(dp(7), dp(2), dp(7), dp(2));
                stBadge.setBackground(roundBox(stBg, 0, 99));
                LinearLayout.LayoutParams sLp = new LinearLayout.LayoutParams(WRAP, WRAP);
                sLp.leftMargin = dp(8);
                row.addView(stBadge, sLp);
            } else {
                TextView lanBadge = label("局域网", 10, cOnPrimaryContainer, true);
                lanBadge.setPadding(dp(7), dp(2), dp(7), dp(2));
                lanBadge.setBackground(roundBox(cPrimaryContainer, 0, 99));
                LinearLayout.LayoutParams sLp = new LinearLayout.LayoutParams(WRAP, WRAP);
                sLp.leftMargin = dp(8);
                row.addView(lanBadge, sLp);
            }

            if (sel) {
                TextView chk = label("✓", 14, cPrimary, true);
                LinearLayout.LayoutParams ckLp = new LinearLayout.LayoutParams(WRAP, WRAP);
                ckLp.leftMargin = dp(8);
                row.addView(chk, ckLp);
            }

            LinearLayout.LayoutParams rLp = new LinearLayout.LayoutParams(MATCH, WRAP);
            if (i > 0) {
                rLp.topMargin = dp(6);
            }
            sharePeersBox.addView(row, rLp);
        }

        String tgtName = resolveTargetName(shareSelectedTarget);
        shareSendBtn.setText("立即发送给 " + tgtName);
    }

    private void clearSharedPending() {
        sharedUris = null;
        if (shareBannerView != null) {
            shareBannerView.setVisibility(View.GONE);
        }
    }

    private void dismissShareSheet(boolean clearPending) {
        if (clearPending) {
            clearSharedPending();
        }
        if (shareDialog != null) {
            try {
                if (shareDialog.isShowing()) {
                    shareDialog.dismiss();
                }
            } catch (Exception ignored) {
            }
            shareDialog = null;
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

    private void pickFolder() {
        final String target = selectedTarget;
        if (!isAutoTarget(target) && !isLanTarget(target)) {
            toast("请先选择或配对接收设备");
            return;
        }
        saveLastPeer(target);
        Intent i = new Intent(Intent.ACTION_OPEN_DOCUMENT_TREE);
        i.addFlags(Intent.FLAG_GRANT_READ_URI_PERMISSION);
        try {
            startActivityForResult(i, PICK_FOLDER);
        } catch (Exception e) {
            toast("无法打开文件夹选择器：" + e.getMessage());
        }
    }

    private void showSendTextDialog(String initialText) {
        final String target = selectedTarget;
        if (!isAutoTarget(target) && !isLanTarget(target)) {
            toast("请先选择或配对接收设备");
            return;
        }
        final String targetName = resolveTargetName(target);
        final EditText input = new EditText(this);
        input.setHint("输入要发送的文字内容…");
        input.setHintTextColor(cOnSurfaceVariant);
        input.setTextColor(cOnSurface);
        input.setTextSize(14);
        input.setInputType(InputType.TYPE_CLASS_TEXT | InputType.TYPE_TEXT_FLAG_MULTI_LINE);
        input.setMinLines(4);
        input.setMaxLines(8);
        input.setGravity(Gravity.TOP | Gravity.START);
        input.setPadding(dp(12), dp(10), dp(12), dp(10));
        input.setBackground(roundBox(cSurface, cBorder, 10));
        if (initialText != null && !initialText.isEmpty()) {
            input.setText(initialText);
            input.setSelection(initialText.length());
        }

        FrameLayout wrap = new FrameLayout(this);
        wrap.setPadding(dp(20), dp(10), dp(20), 0);
        wrap.addView(input, new FrameLayout.LayoutParams(MATCH, WRAP));

        new AlertDialog.Builder(this)
                .setTitle("发文字给 " + targetName)
                .setView(wrap)
                .setNegativeButton("取消", null)
                .setPositiveButton("发送", (d, w) -> {
                    String text = input.getText().toString();
                    if (text.trim().isEmpty()) {
                        toast("请输入要发送的文字");
                        return;
                    }
                    sendTextContent(text, "text", "正在发送文字…");
                })
                .show();
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
        sendTextContent(text, "clipboard", "正在发送剪贴板文本…");
    }

    private void sendTextContent(final String text, final String prefix, String busyMsg) {
        final String target = selectedTarget;
        if (!isAutoTarget(target) && !isLanTarget(target)) {
            toast("请先选择或配对接收设备");
            return;
        }
        saveLastPeer(target);
        final String name = deviceName;
        final String targetName = resolveTargetName(target);
        final boolean auto = isAutoTarget(target);
        setSendBusy(true, busyMsg);
        new Thread(() -> {
            File dir = new File(getCacheDir(), "text-" + System.nanoTime());
            dir.mkdirs();
            String ts = new SimpleDateFormat("HHmmss", Locale.US).format(new Date());
            File f = new File(dir, prefix + "-" + ts + ".txt");
            try (OutputStream out = new FileOutputStream(f)) {
                out.write(text.getBytes(StandardCharsets.UTF_8));
                List<File> list = new ArrayList<>();
                list.add(f);
                sendPreparedFiles(list, dir, name, target, targetName, auto);
            } catch (Exception e) {
                Log.e(TAG, "sendTextContent:" + prefix, e);
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

    private void sendFolderTree(final Uri treeUri, final String name, final String target, final String targetName) {
        final boolean auto = isAutoTarget(target);
        setSendBusy(true, "正在扫描文件夹…");
        new Thread(() -> {
            File batchDir = new File(new File(getCacheDir(), "outgoing"), String.valueOf(System.nanoTime()));
            try {
                String rootDocId = DocumentsContract.getTreeDocumentId(treeUri);
                String rootName = sanitizeFolderSeg(resolveTreeRootName(treeUri, rootDocId), "folder");
                File rootDir = new File(batchDir, rootName);
                int stagedCount = stageTreeRecursive(treeUri, rootDocId, rootDir);
                if (stagedCount <= 0) {
                    deleteRecursive(batchDir);
                    onSendFailed("所选文件夹为空");
                    return;
                }
                sendPreparedFiles(java.util.Collections.singletonList(rootDir), batchDir, name, target, targetName, auto);
            } catch (Exception e) {
                Log.e(TAG, "sendFolderTree", e);
                deleteRecursive(batchDir);
                onSendFailed(e.getMessage());
            }
        }).start();
    }

    private int stageTreeRecursive(Uri treeUri, String parentDocId, File destDir) throws Exception {
        Uri childrenUri = DocumentsContract.buildChildDocumentsUriUsingTree(treeUri, parentDocId);
        String[] cols = new String[]{
                DocumentsContract.Document.COLUMN_DOCUMENT_ID,
                DocumentsContract.Document.COLUMN_DISPLAY_NAME,
                DocumentsContract.Document.COLUMN_MIME_TYPE
        };
        int count = 0;
        android.content.ContentResolver cr = getContentResolver();
        try (Cursor c = cr.query(childrenUri, cols, null, null, null)) {
            if (c == null) {
                return 0;
            }
            while (c.moveToNext()) {
                String docId = c.getString(0);
                String rawName = c.getString(1);
                String mime = c.getString(2);
                if (docId == null || docId.isEmpty()) {
                    continue;
                }
                String safeName = sanitizeFolderSeg(rawName, "item");
                if (DocumentsContract.Document.MIME_TYPE_DIR.equals(mime)) {
                    count += stageTreeRecursive(treeUri, docId, new File(destDir, safeName));
                } else {
                    if (!destDir.exists() && !destDir.mkdirs()) {
                        throw new Exception("无法创建临时目录");
                    }
                    File outFile = uniqueStagedFile(destDir, safeName);
                    Uri docUri = DocumentsContract.buildDocumentUriUsingTree(treeUri, docId);
                    try (InputStream in = cr.openInputStream(docUri);
                         OutputStream out = new FileOutputStream(outFile)) {
                        if (in == null) {
                            throw new Exception("无法读取 " + safeName);
                        }
                        copyStream(in, out);
                    }
                    count++;
                }
            }
        }
        return count;
    }

    private String resolveTreeRootName(Uri treeUri, String rootDocId) {
        try {
            Uri docUri = DocumentsContract.buildDocumentUriUsingTree(treeUri, rootDocId);
            try (Cursor c = getContentResolver().query(docUri,
                    new String[]{DocumentsContract.Document.COLUMN_DISPLAY_NAME}, null, null, null)) {
                if (c != null && c.moveToFirst()) {
                    String n = c.getString(0);
                    if (n != null && !n.trim().isEmpty()) {
                        return n.trim();
                    }
                }
            }
        } catch (Exception ignored) {
        }
        if (rootDocId != null) {
            int colon = rootDocId.lastIndexOf(':');
            String tail = colon >= 0 ? rootDocId.substring(colon + 1) : rootDocId;
            int slash = Math.max(tail.lastIndexOf('/'), tail.lastIndexOf('\\'));
            if (slash >= 0 && slash + 1 < tail.length()) {
                tail = tail.substring(slash + 1);
            }
            if (!tail.trim().isEmpty()) {
                return tail.trim();
            }
        }
        return "folder";
    }

    private static String sanitizeFolderSeg(String raw, String fallback) {
        if (raw == null) {
            return fallback;
        }
        String s = raw.replace('/', '_').replace('\\', '_').replace(':', '_').trim();
        if (s.isEmpty() || ".".equals(s) || "..".equals(s)) {
            return fallback;
        }
        return s;
    }

    private static File uniqueStagedFile(File dir, String name) {
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
        return new File(dir, System.nanoTime() + "_" + name);
    }

    private static int countRegularFiles(File f) {
        if (f == null || !f.exists()) {
            return 0;
        }
        if (f.isFile()) {
            return 1;
        }
        if (!f.isDirectory()) {
            return 0;
        }
        int n = 0;
        File[] children = f.listFiles();
        if (children != null) {
            for (File c : children) {
                n += countRegularFiles(c);
            }
        }
        return n;
    }

    private static long sumRegularBytes(File f) {
        if (f == null || !f.exists()) {
            return 0L;
        }
        if (f.isFile()) {
            return f.length();
        }
        if (!f.isDirectory()) {
            return 0L;
        }
        long sum = 0L;
        File[] children = f.listFiles();
        if (children != null) {
            for (File c : children) {
                sum += sumRegularBytes(c);
            }
        }
        return sum;
    }

    private void sendPreparedFiles(List<File> files, File rootTmp, String name, String target,
                                   String targetName, boolean auto) {
        String recTitle = "";
        long totalBytes = 0;
        if (files != null && !files.isEmpty()) {
            int fileCount = 0;
            for (File f : files) {
                if (f != null) {
                    fileCount += countRegularFiles(f);
                    totalBytes += sumRegularBytes(f);
                }
            }
            File first = files.get(0);
            String firstName = first != null ? first.getName() : "";
            if (files.size() == 1 && first != null && first.isDirectory()) {
                recTitle = firstName + " (" + fileCount + " 个文件)";
            } else {
                recTitle = fileCount > 1 ? (firstName + " 等 " + fileCount + " 个文件") : firstName;
            }
        }
        try {
            JSONArray paths = new JSONArray();
            for (File f : files) {
                paths.put(f.getAbsolutePath());
            }
            final String summary = auto
                    ? Mobile.sendAuto(TransferService.getSignalUrl(MainActivity.this), keyFile().getAbsolutePath(),
                            pairedFile().getAbsolutePath(), target, name, paths.toString(), lanHint(),
                            TransferService.getStunServers(MainActivity.this))
                    : Mobile.sendFilesWithKey(target, name, keyFile().getAbsolutePath(), paths.toString());
            onSendSucceeded(summary, targetName, recTitle, totalBytes);
        } catch (Exception e) {
            Log.e(TAG, auto ? "sendAuto" : "sendLan", e);
            onSendFailed(e.getMessage(), recTitle, totalBytes, targetName);
        } finally {
            for (File f : files) {
                if (f != null) {
                    deleteTree(f, f.getParentFile());
                }
            }
            if (rootTmp != null) {
                deleteRecursive(rootTmp);
            }
        }
    }

    private void setSendBusy(boolean busy, String msg) {
        sendBusyFlag = busy;
        if (sendPickTile != null) {
            sendPickTile.setEnabled(!busy);
            sendPickTile.setAlpha(busy ? 0.6f : 1f);
        }
        if (sendFolderTile != null) {
            sendFolderTile.setEnabled(!busy);
            sendFolderTile.setAlpha(busy ? 0.6f : 1f);
        }
        if (sendTextTile != null) {
            sendTextTile.setEnabled(!busy);
            sendTextTile.setAlpha(busy ? 0.6f : 1f);
        }
        if (sendClipTile != null) {
            sendClipTile.setEnabled(!busy);
            sendClipTile.setAlpha(busy ? 0.6f : 1f);
        }
        if (busy) {
            startKeepAlive(msg);
            showSendBanner("", msg, false);
            startProgressPolling();
        } else {
            pollActiveTransferOnce();
        }
    }

    private void startProgressPolling() {
        if (progressPollingActive) {
            return;
        }
        progressPollingActive = true;
        ui.removeCallbacks(progressPollTask);
        ui.post(progressPollTask);
    }

    private void stopProgressPolling() {
        progressPollingActive = false;
        ui.removeCallbacks(progressPollTask);
    }

    private void pollActiveTransferOnce() {
        ioPool.execute(() -> {
            try {
                final String raw = Mobile.activeTransferStatus();
                ui.post(() -> applyActiveTransferJson(raw));
            } catch (Exception e) {
                Log.w(TAG, "activeTransferStatus", e);
            }
        });
    }

    private void applyActiveTransferJson(String raw) {
        if (activeTransferCard == null || raw == null || raw.trim().isEmpty()) {
            return;
        }
        try {
            JSONObject o = new JSONObject(raw);
            boolean active = o.optBoolean("active", false);
            if (!active) {
                activeTransferCard.setVisibility(View.GONE);
                if (tempTxBox != null) {
                    tempTxBox.setVisibility(View.GONE);
                }
                if (tempJoinTxBox != null) {
                    tempJoinTxBox.setVisibility(View.GONE);
                }
                if (!sendBusyFlag) {
                    stopProgressPolling();
                }
                return;
            }
            if (!progressPollingActive) {
                startProgressPolling();
            }

            String dir = o.optString("direction", "send");
            String peer = o.optString("peer", "");
            String stage = o.optString("stage", "");
            String channel = o.optString("channel", "");
            int fIdx = o.optInt("file_index", 0);
            int fCnt = o.optInt("file_count", 0);
            String fName = o.optString("file_name", "");
            long done = o.optLong("bytes_done", 0);
            long total = o.optLong("bytes_total", 0);
            long speedBps = o.optLong("speed_bps", 0);
            long startedAt = o.optLong("started_at", 0);
            boolean localPaused = o.optBoolean("paused", false) || "paused".equals(stage);
            boolean peerPaused = o.optBoolean("peer_paused", false) || "peer_paused".equals(stage);
            boolean anyPaused = localPaused || peerPaused;
            boolean canPause = o.optBoolean("can_pause", false);
            boolean canCancel = o.optBoolean("can_cancel", false);

            String head = "recv".equals(dir) ? "正在接收" : "正在发送";
            if (!peer.isEmpty()) {
                head += " · " + displayPeerLabel(peer);
            }
            txHeadTv.setText(head);

            if (!channel.isEmpty()) {
                txChanBadge.setVisibility(View.VISIBLE);
                stylePathBadge(txChanBadge, channel);
            } else {
                txChanBadge.setVisibility(View.GONE);
            }

            StringBuilder sb = new StringBuilder(stageLabel(stage));
            if (fCnt > 1 && fIdx > 0) {
                sb.append(" · 第 ").append(fIdx).append("/").append(fCnt).append(" 个");
            }
            if (!fName.isEmpty()) {
                sb.append(" · ").append(fName);
            }
            txSubTv.setText(sb.toString());

            boolean determinate = total > 0 && ("transferring".equals(stage) || "verifying".equals(stage)
                    || "paused".equals(stage) || "peer_paused".equals(stage) || done > 0);
            int progressPromille = 0;
            if (determinate) {
                progressPromille = (int) Math.max(0, Math.min(1000, (done * 1000L) / total));
                txProgressBar.setIndeterminate(false);
                txProgressBar.setProgress(progressPromille);
            } else {
                txProgressBar.setIndeterminate(true);
            }
            int barTint = anyPaused ? cWarn : cPrimary;
            txProgressBar.setProgressTintList(ColorStateList.valueOf(barTint));
            txProgressBar.setIndeterminateTintList(ColorStateList.valueOf(barTint));

            long nowMs = System.currentTimeMillis();
            long elapsedMs = startedAt > 0 && nowMs > startedAt ? nowMs - startedAt : 0L;
            if (speedBps <= 0L && done > 0L && elapsedMs >= 500L && !"verifying".equals(stage) && !anyPaused) {
                speedBps = (done * 1000L) / elapsedMs;
            }
            StringBuilder mb = new StringBuilder();
            if (total > 0) {
                int pct = (int) Math.max(0, Math.min(100, (done * 100L) / total));
                mb.append(pct).append("% · ").append(fmtBytes(done)).append(" / ").append(fmtBytes(total));
            } else if (done > 0) {
                mb.append("已传输 ").append(fmtBytes(done));
            } else {
                mb.append("正在建立传输通道…");
            }
            if (localPaused) {
                mb.append(" · 已暂停");
            } else if (peerPaused) {
                mb.append(" · 对方已暂停");
            } else if (speedBps > 0L && !"verifying".equals(stage)) {
                mb.append(" · ").append(fmtSpeed(speedBps));
            }
            if (elapsedMs >= 1000L) {
                mb.append(" · 已用 ").append(formatDurationSec(elapsedMs / 1000L));
            }
            if (total > 0 && done > 0 && done < total && !"verifying".equals(stage) && !anyPaused) {
                long remSec = -1L;
                if (speedBps > 0L) {
                    remSec = (total - done) / speedBps;
                } else if (elapsedMs >= 1200L) {
                    remSec = ((total - done) * elapsedMs) / (done * 1000L);
                }
                if (remSec >= 0L && remSec < 86400L) {
                    mb.append(" · 预计剩余 ").append(formatDurationSec(remSec));
                }
            }
            String metricsText = mb.toString();
            txMetricsTv.setText(metricsText);

            boolean showPause = canPause && !"verifying".equals(stage);
            String pauseBtnText = localPaused ? "继续" : (peerPaused ? "对方已暂停" : "暂停");
            boolean pauseBtnEnabled = localPaused || !peerPaused;
            if (txActionRow != null) {
                txActionRow.setVisibility((showPause || canCancel) ? View.VISIBLE : View.GONE);
                if (txPauseBtn != null) {
                    txPauseBtn.setVisibility(showPause ? View.VISIBLE : View.GONE);
                    txPauseBtn.setText(pauseBtnText);
                    txPauseBtn.setEnabled(pauseBtnEnabled);
                    txPauseBtn.setAlpha(pauseBtnEnabled ? 1.0f : 0.55f);
                }
                if (txCancelBtn != null) {
                    txCancelBtn.setVisibility(canCancel ? View.VISIBLE : View.GONE);
                }
            }
            activeTransferCard.setVisibility(View.VISIBLE);

            String dialogTitle = (localPaused ? "已暂停" : (peerPaused ? "对方已暂停" : ("recv".equals(dir) ? "接收中" : "发送中")))
                    + (channel.isEmpty() ? "" : "（" + pathLabel(channel) + "）")
                    + (fName.isEmpty() ? "" : " · " + fName);
            updateTempDialogProgress(tempTxBox, tempTxTitleTv, tempTxBar, tempTxMetaTv,
                    dialogTitle, determinate, progressPromille, metricsText);
            updateTempDialogProgress(tempJoinTxBox, tempJoinTxTitleTv, tempJoinTxBar, tempJoinTxMetaTv,
                    dialogTitle, determinate, progressPromille, metricsText);
            if (tempJoinActionRow != null) {
                tempJoinActionRow.setVisibility((showPause || canCancel) ? View.VISIBLE : View.GONE);
                if (tempJoinPauseBtn != null) {
                    tempJoinPauseBtn.setVisibility(showPause ? View.VISIBLE : View.GONE);
                    tempJoinPauseBtn.setText(pauseBtnText);
                    tempJoinPauseBtn.setEnabled(pauseBtnEnabled);
                    tempJoinPauseBtn.setAlpha(pauseBtnEnabled ? 1.0f : 0.55f);
                }
                if (tempJoinCancelBtn != null) {
                    tempJoinCancelBtn.setVisibility(canCancel ? View.VISIBLE : View.GONE);
                }
            }
        } catch (Exception e) {
            Log.w(TAG, "applyActiveTransferJson", e);
        }
    }

    private void toggleActiveTransferPause() {
        ioPool.execute(() -> {
            try {
                Mobile.togglePauseActiveTransfer();
                pollActiveTransferOnce();
            } catch (Exception e) {
                Log.w(TAG, "togglePauseActiveTransfer", e);
            }
        });
    }

    private void cancelActiveTransfer() {
        ioPool.execute(() -> {
            try {
                Mobile.cancelActiveTransfer();
                pollActiveTransferOnce();
            } catch (Exception e) {
                Log.w(TAG, "cancelActiveTransfer", e);
            }
        });
    }

    private static void updateTempDialogProgress(LinearLayout box, TextView titleTv, ProgressBar bar, TextView metaTv,
                                                 String title, boolean determinate, int progressPromille, String meta) {
        if (box == null || titleTv == null || bar == null || metaTv == null) {
            return;
        }
        box.setVisibility(View.VISIBLE);
        titleTv.setText(title);
        if (determinate) {
            bar.setIndeterminate(false);
            bar.setProgress(progressPromille);
        } else {
            bar.setIndeterminate(true);
        }
        metaTv.setText(meta);
    }

    private static String stageLabel(String stage) {
        if ("connecting".equals(stage)) {
            return "连接中";
        }
        if ("waiting".equals(stage)) {
            return "等待对方确认";
        }
        if ("racing".equals(stage)) {
            return "协商直连路径";
        }
        if ("transferring".equals(stage)) {
            return "传输中";
        }
        if ("verifying".equals(stage)) {
            return "校验写入中";
        }
        if ("paused".equals(stage)) {
            return "已暂停";
        }
        if ("peer_paused".equals(stage)) {
            return "对方已暂停";
        }
        if ("cancelling".equals(stage)) {
            return "正在取消";
        }
        return "处理中";
    }

    private static String formatDurationSec(long sec) {
        long m = sec / 60L;
        long s = sec % 60L;
        if (m >= 60L) {
            return String.format(Locale.US, "%d:%02d:%02d", m / 60L, m % 60L, s);
        }
        return String.format(Locale.US, "%02d:%02d", m, s);
    }

    private void onSendSucceeded(final String raw, final String targetName,
                                 final String recTitle, final long totalBytes) {
        ui.post(() -> {
            setSendBusy(false, "");
            if (recvRunning || webRunning) {
                startKeepAlive(recvRunning ? "接收中" : "网页快传中");
            } else {
                stopKeepAlive();
            }
            loadPairedPeers();
            try {
                JSONObject o = new JSONObject(raw);
                if (!o.optBoolean("ok", false)) {
                    String errMsg = o.optString("error", raw);
                    recordSendOutcome(errMsg, recTitle, totalBytes, targetName);
                    return;
                }
                String path = o.optString("path", "");
                JSONArray files = o.optJSONArray("files");
                int n = files != null ? files.length() : 0;
                long bytes = o.optLong("bytes", 0);
                long durMs = o.optLong("duration_ms", 0);
                String first = n > 0 ? files.optString(0, "已发送文件") : "已发送文件";
                String finalTitle = n > 1 ? first + " 等 " + n + " 个文件" : first;
                addRecentEntry(new ReceivedEntry(finalTitle, "", "", path, bytes,
                        System.currentTimeMillis(), "send", targetName));
                String spd = (durMs >= 200L && bytes > 0L) ? (" · " + fmtSpeed((bytes * 1000L) / durMs)) : "";
                showSendBanner(path, "已发送 · " + finalTitle + " (" + fmtBytes(bytes) + spd + ")", false);
            } catch (Exception e) {
                showSendBanner("", raw, false);
            }
        });
    }

    private void onSendFailed(final String err) {
        onSendFailed(err, "", 0, "");
    }

    private void onSendFailed(final String err, final String recTitle, final long totalBytes, final String targetName) {
        ui.post(() -> {
            setSendBusy(false, "");
            if (recvRunning || webRunning) {
                startKeepAlive(recvRunning ? "接收中" : "网页快传中");
            } else {
                stopKeepAlive();
            }
            recordSendOutcome(err, recTitle, totalBytes, targetName);
        });
    }

    private void recordSendOutcome(String err, String recTitle, long totalBytes, String targetName) {
        String errText = err == null ? "" : err;
        String status;
        if (errText.contains("对方已取消")) {
            status = "peer_cancelled";
            showSendBanner("peer_cancelled", "对方已取消传输", false);
        } else if (errText.contains("已取消")) {
            status = "cancelled";
            showSendBanner("cancelled", "已取消发送", false);
        } else {
            status = "failed";
            showSendBanner("fail", "发送失败：" + errText, true);
        }
        String entryTitle = (recTitle != null && !recTitle.isEmpty()) ? recTitle : "发送文件";
        addRecentEntry(new ReceivedEntry(entryTitle, "", "", "", totalBytes,
                System.currentTimeMillis(), "send", targetName, status, errText));
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
        if (requestCode == PICK_DOWNLOAD_DIR) {
            Uri treeUri = data.getData();
            if (treeUri != null) {
                final int takeFlags = Intent.FLAG_GRANT_READ_URI_PERMISSION | Intent.FLAG_GRANT_WRITE_URI_PERMISSION;
                try {
                    getContentResolver().takePersistableUriPermission(treeUri, takeFlags);
                } catch (Exception e) {
                    Log.w(TAG, "takePersistableUriPermission", e);
                }
                customDownloadTreeUri = treeUri.toString();
                customDownloadLabel = formatTreeUriLabel(treeUri);
                getPreferences(MODE_PRIVATE).edit()
                        .putString("customDownloadTreeUri", customDownloadTreeUri)
                        .putString("customDownloadLabel", customDownloadLabel)
                        .apply();
                refreshDownloadDirUi();
                toast("下载目录已更改为：" + customDownloadLabel);
            }
            return;
        }
        if (requestCode == PICK_TEMP_SEND || requestCode == PICK_TEMP_UPLOAD) {
            List<Uri> picked = new ArrayList<>();
            if (data.getClipData() != null) {
                for (int i = 0; i < data.getClipData().getItemCount(); i++) {
                    picked.add(data.getClipData().getItemAt(i).getUri());
                }
            } else if (data.getData() != null) {
                picked.add(data.getData());
            }
            if (!picked.isEmpty()) {
                if (requestCode == PICK_TEMP_SEND) {
                    startTempSendFromUris(picked);
                } else {
                    uploadTempJoinUris(picked);
                }
            }
            return;
        }
        if (requestCode == PICK_FOLDER) {
            Uri treeUri = data.getData();
            if (treeUri != null) {
                sendFolderTree(treeUri, deviceName, selectedTarget, resolveTargetName(selectedTarget));
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
                    entries.add(new PeerEntry(name, id, id));
                }
                ui.post(() -> {
                    pairedEntries.clear();
                    pairedEntries.addAll(entries);
                    prunePairedLanEntries();
                    if (selectedTarget.isEmpty() && !entries.isEmpty()) {
                        selectedTarget = entries.get(0).value;
                        saveLastPeer(selectedTarget);
                    }
                    renderPeerSection();
                    if (shareDialog != null && shareDialog.isShowing()) {
                        refreshShareSheetPeers();
                    }
                });
            } catch (Exception e) {
                Log.w(TAG, "loadPairedPeers", e);
            }
        });
    }

    private void prunePairedLanEntries() {
        Map<String, String> pairedById = new HashMap<>();
        for (PeerEntry p : pairedEntries) {
            if (!p.deviceId.isEmpty()) {
                pairedById.put(p.deviceId, p.value);
            }
        }
        Iterator<PeerEntry> it = lanEntries.iterator();
        while (it.hasNext()) {
            PeerEntry e = it.next();
            if (!e.deviceId.isEmpty() && pairedById.containsKey(e.deviceId)) {
                String pairedVal = pairedById.get(e.deviceId);
                lanOnlineSet.add(pairedVal);
                if (e.value.equals(selectedTarget)) {
                    selectedTarget = pairedVal;
                    saveLastPeer(selectedTarget);
                }
                if (e.value.equals(shareSelectedTarget)) {
                    shareSelectedTarget = pairedVal;
                }
                it.remove();
            }
        }
    }

    private void discover() {
        runDiscover(false);
    }

    private void discoverSilent() {
        runDiscover(true);
    }

    private void runDiscover(final boolean quiet) {
        if (lanSearching) {
            return;
        }
        final String hint = lanHint();
        if (hint == null || hint.trim().isEmpty()) {
            if (!quiet) {
                toast("当前未连接 Wi-Fi，无法搜索局域网设备");
            }
            return;
        }
        lanSearching = true;
        renderPeerSection();
        final String name = deviceName;
        new Thread(() -> {
            try {
                String json = Mobile.discover(name, 3000, hint);
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
                    String id = o.optString("ID", "").trim().toLowerCase(Locale.US);
                    found.add(new PeerEntry(dn + " (LAN)", addr, id));
                }
                ui.post(() -> {
                    lanSearching = false;
                    Map<String, String> pairedById = new HashMap<>();
                    for (PeerEntry p : pairedEntries) {
                        if (!p.deviceId.isEmpty()) {
                            pairedById.put(p.deviceId, p.value);
                        }
                    }
                    List<PeerEntry> unpairedFound = new ArrayList<>();
                    int pairedFoundCount = 0;
                    for (PeerEntry e : found) {
                        if (!e.deviceId.isEmpty() && pairedById.containsKey(e.deviceId)) {
                            pairedFoundCount++;
                            lanOnlineSet.add(pairedById.get(e.deviceId));
                        } else {
                            unpairedFound.add(e);
                        }
                    }
                    lanEntries.clear();
                    lanEntries.addAll(unpairedFound);
                    if (!unpairedFound.isEmpty()) {
                        if (selectedTarget.isEmpty()) {
                            selectedTarget = unpairedFound.get(0).value;
                        }
                    } else if (!quiet) {
                        if (pairedFoundCount > 0) {
                            toast("已在局域网发现 " + pairedFoundCount + " 台已配对设备");
                        } else {
                            toast("同一局域网暂无可直连设备");
                        }
                    }
                    renderPeerSection();
                    if (shareDialog != null && shareDialog.isShowing()) {
                        refreshShareSheetPeers();
                    }
                });
            } catch (Exception e) {
                ui.post(() -> {
                    lanSearching = false;
                    renderPeerSection();
                    if (!quiet) {
                        toast("局域网发现失败：" + e.getMessage());
                    }
                });
            }
        }).start();
    }

    private String pickSmartDefaultTarget(List<PeerEntry> combined) {
        if (combined.isEmpty()) {
            return selectedTarget;
        }
        boolean foundSel = false;
        for (PeerEntry e : combined) {
            if (e.value.equals(selectedTarget)) {
                foundSel = true;
                break;
            }
        }
        if (userPickedTarget && foundSel) {
            return selectedTarget;
        }
        for (PeerEntry e : pairedEntries) {
            if (lanOnlineSet.contains(e.value)) {
                return e.value;
            }
        }
        if ("online".equals(signalState)) {
            for (PeerEntry e : pairedEntries) {
                if (cloudOnlineSet.contains(e.value)) {
                    return e.value;
                }
            }
        }
        if (foundSel) {
            return selectedTarget;
        }
        return combined.get(0).value;
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

        if (!combined.isEmpty()) {
            String chosen = pickSmartDefaultTarget(combined);
            if (!chosen.equals(selectedTarget)) {
                selectedTarget = chosen;
                if (HEX64.matcher(selectedTarget).matches()) {
                    saveLastPeer(selectedTarget);
                }
            }
        }
        refreshTargetStatusUi();

        peerChipRow.removeAllViews();
        for (int i = 0; i < combined.size(); i++) {
            final PeerEntry entry = combined.get(i);
            final boolean sel = entry.value.equals(selectedTarget);
            final boolean isLan = !isAutoTarget(entry.value) || lanOnlineSet.contains(entry.value);
            final boolean isCloud = !isLan && "online".equals(signalState) && cloudOnlineSet.contains(entry.value);
            int dotClr = isLan ? Color.parseColor("#10B981") : (isCloud ? cPrimary : cOutline);
            if (sel && isLan) {
                dotClr = Color.parseColor("#6EE7B7");
            } else if (sel && isCloud) {
                dotClr = Color.parseColor("#A7F3D0");
            }

            LinearLayout chip = new LinearLayout(this);
            chip.setOrientation(LinearLayout.HORIZONTAL);
            chip.setGravity(Gravity.CENTER_VERTICAL);
            chip.setPadding(dp(12), dp(7), dp(14), dp(7));
            chip.setBackground(ripple(roundBox(
                    sel ? cPrimaryPressed : cCard,
                    sel ? cPrimaryPressed : cBorder, 99), 99));

            View dot = new View(this);
            dot.setBackground(oval(dotClr));
            LinearLayout.LayoutParams dLp = new LinearLayout.LayoutParams(dp(7), dp(7));
            dLp.rightMargin = dp(6);
            chip.addView(dot, dLp);

            TextView tv = label(entry.name, 12, sel ? cOnPrimary : cOnSurface, true);
            tv.setSingleLine(true);
            chip.addView(tv, new LinearLayout.LayoutParams(WRAP, WRAP));

            chip.setOnClickListener(v -> {
                selectedTarget = entry.value;
                userPickedTarget = true;
                saveLastPeer(selectedTarget);
                renderPeerSection();
            });
            LinearLayout.LayoutParams lp = new LinearLayout.LayoutParams(WRAP, WRAP);
            lp.rightMargin = dp(8);
            peerChipRow.addView(chip, lp);
        }

        // Unified Temporary Share chip right inside target bar
        TextView tempChip = label("+ 临时扫码传", 12, cPrimary, true);
        tempChip.setPadding(dp(12), dp(7), dp(12), dp(7));
        tempChip.setBackground(ripple(roundBox(cCard, cBorder, 99), 99));
        tempChip.setOnClickListener(v -> openTempShareSheet());
        LinearLayout.LayoutParams tLp = new LinearLayout.LayoutParams(WRAP, WRAP);
        tLp.rightMargin = dp(8);
        peerChipRow.addView(tempChip, tLp);

        TextView addBtn = label("+", 16, cOnSurface, true);
        addBtn.setGravity(Gravity.CENTER);
        addBtn.setBackground(ripple(roundBox(cCard, cBorder, 99), 99));
        addBtn.setContentDescription("添加设备");
        addBtn.setOnClickListener(v -> openPairSheet(0));
        LinearLayout.LayoutParams addLp = new LinearLayout.LayoutParams(dp(32), dp(32));
        addLp.rightMargin = dp(8);
        peerChipRow.addView(addBtn, addLp);

        TextView lanBtn = label(lanSearching ? "搜索中…" : "局域网", 12, cOnSurfaceVariant, false);
        lanBtn.setPadding(dp(11), dp(7), dp(11), dp(7));
        lanBtn.setBackground(ripple(roundBox(cCard, cBorder, 99), 99));
        lanBtn.setOnClickListener(v -> discover());
        peerChipRow.addView(lanBtn, new LinearLayout.LayoutParams(WRAP, WRAP));
        renderDevicesPage();
    }

    private String displayPeerLabel(String peer) {
        if (peer == null || peer.isEmpty()) {
            return "";
        }
        for (PeerEntry e : pairedEntries) {
            if (e.value.equals(peer)) {
                return e.name;
            }
        }
        for (PeerEntry e : lanEntries) {
            if (e.value.equals(peer)) {
                return e.name;
            }
        }
        if (HEX64.matcher(peer).matches()) {
            return "设备 " + peer.substring(0, 12);
        }
        return peer;
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
        if (!last.isEmpty() && selectedTarget.isEmpty() && HEX64.matcher(last).matches()) {
            selectedTarget = last;
        }
    }

    private void saveLastPeer(String peer) {
        if (peer == null || peer.isEmpty() || HEX64.matcher(peer).matches()) {
            getPreferences(MODE_PRIVATE).edit().putString("lastPeer", peer == null ? "" : peer).apply();
        }
    }

    private String loadSavedName() {
        return TransferService.loadDeviceName(this);
    }

    private String defaultName() {
        return TransferService.defaultDeviceName(this);
    }

    private static String trimToUtf8Bytes(String s, int maxBytes) {
        return TransferService.trimToUtf8Bytes(s, maxBytes);
    }

    // ================= Models & Custom Views =================

    private static class PeerEntry {
        final String name;
        final String value;
        final String deviceId;

        PeerEntry(String name, String value) {
            this(name, value, "");
        }

        PeerEntry(String name, String value, String deviceId) {
            this.name = name;
            this.value = value;
            this.deviceId = deviceId == null ? "" : deviceId.trim().toLowerCase(Locale.US);
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
        final String status; // "" | "ok" | "cancelled" | "peer_cancelled" | "failed"
        final String error;

        ReceivedEntry(String name, String uri, String mime, String via, long bytes, long ts,
                      String dir, String peer) {
            this(name, uri, mime, via, bytes, ts, dir, peer, "", "");
        }

        ReceivedEntry(String name, String uri, String mime, String via, long bytes, long ts,
                      String dir, String peer, String status, String error) {
            this.name = name;
            this.uri = uri;
            this.mime = mime;
            this.via = via;
            this.bytes = bytes;
            this.ts = ts;
            this.dir = (dir == null || dir.isEmpty()) ? "recv" : dir;
            this.peer = peer == null ? "" : peer;
            this.status = status == null ? "" : status;
            this.error = error == null ? "" : error;
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
        static final int TEXT = 5;
        static final int TRANSFER = 6;
        static final int DEVICES = 7;
        static final int FOLDER = 8;
        static final int INFO = 9;
        static final int HISTORY = 10;
        static final int SEND = 11;

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
        public int getIntrinsicWidth() {
            return dp(24);
        }

        @Override
        public int getIntrinsicHeight() {
            return dp(24);
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
                float cx = w * 0.5f, cy = h * 0.5f;
                float rIn = w * 0.27f, rOut = w * 0.39f;
                double da1 = 0.22, da2 = 0.13;
                for (int i = 0; i < 8; i++) {
                    double a = i * Math.PI / 4.0;
                    float x0 = cx + (float) Math.cos(a - da1) * rIn;
                    float y0 = cy + (float) Math.sin(a - da1) * rIn;
                    float x1 = cx + (float) Math.cos(a - da2) * rOut;
                    float y1 = cy + (float) Math.sin(a - da2) * rOut;
                    float x2 = cx + (float) Math.cos(a + da2) * rOut;
                    float y2 = cy + (float) Math.sin(a + da2) * rOut;
                    float x3 = cx + (float) Math.cos(a + da1) * rIn;
                    float y3 = cy + (float) Math.sin(a + da1) * rIn;
                    if (i == 0) {
                        p.moveTo(x0, y0);
                    } else {
                        p.lineTo(x0, y0);
                    }
                    p.lineTo(x1, y1);
                    p.lineTo(x2, y2);
                    p.lineTo(x3, y3);
                }
                p.close();
                c.drawPath(p, paint);
                c.drawCircle(cx, cy, w * 0.12f, paint);
            } else if (kind == TRANSFER) {
                p.moveTo(w * 0.20f, h * 0.34f); p.lineTo(w * 0.80f, h * 0.34f);
                p.moveTo(w * 0.64f, h * 0.20f); p.lineTo(w * 0.80f, h * 0.34f); p.lineTo(w * 0.64f, h * 0.48f);
                p.moveTo(w * 0.80f, h * 0.66f); p.lineTo(w * 0.20f, h * 0.66f);
                p.moveTo(w * 0.36f, h * 0.52f); p.lineTo(w * 0.20f, h * 0.66f); p.lineTo(w * 0.36f, h * 0.80f);
                c.drawPath(p, paint);
            } else if (kind == DEVICES) {
                c.drawRoundRect(new RectF(w * 0.14f, h * 0.24f, w * 0.56f, h * 0.60f),
                        w * 0.06f, h * 0.06f, paint);
                c.drawLine(w * 0.35f, h * 0.60f, w * 0.35f, h * 0.72f, paint);
                c.drawLine(w * 0.22f, h * 0.72f, w * 0.48f, h * 0.72f, paint);
                c.drawRoundRect(new RectF(w * 0.64f, h * 0.32f, w * 0.88f, h * 0.80f),
                        w * 0.06f, h * 0.06f, paint);
                c.drawLine(w * 0.73f, h * 0.71f, w * 0.79f, h * 0.71f, paint);
            } else if (kind == UPLOAD) {
                p.moveTo(w * 0.5f, h * 0.62f); p.lineTo(w * 0.5f, h * 0.18f);
                p.moveTo(w * 0.30f, h * 0.36f); p.lineTo(w * 0.5f, h * 0.17f); p.lineTo(w * 0.70f, h * 0.36f);
                p.moveTo(w * 0.20f, h * 0.65f); p.lineTo(w * 0.20f, h * 0.80f);
                p.lineTo(w * 0.80f, h * 0.80f); p.lineTo(w * 0.80f, h * 0.65f);
                c.drawPath(p, paint);
            } else if (kind == TEXT) {
                c.drawRoundRect(new RectF(w * 0.18f, h * 0.22f, w * 0.82f, h * 0.78f),
                        w * 0.12f, h * 0.12f, paint);
                c.drawLine(w * 0.32f, h * 0.42f, w * 0.68f, h * 0.42f, paint);
                c.drawLine(w * 0.32f, h * 0.58f, w * 0.56f, h * 0.58f, paint);
            } else if (kind == FOLDER) {
                p.moveTo(w * 0.16f, h * 0.28f);
                p.lineTo(w * 0.42f, h * 0.28f);
                p.lineTo(w * 0.50f, h * 0.36f);
                p.lineTo(w * 0.84f, h * 0.36f);
                p.lineTo(w * 0.84f, h * 0.76f);
                p.lineTo(w * 0.16f, h * 0.76f);
                p.close();
                c.drawPath(p, paint);
            } else if (kind == INFO) {
                c.drawCircle(w * 0.5f, h * 0.5f, w * 0.34f, paint);
                c.drawLine(w * 0.5f, h * 0.46f, w * 0.5f, h * 0.68f, paint);
                c.drawPoint(w * 0.5f, h * 0.33f, paint);
            } else if (kind == HISTORY) {
                c.drawCircle(w * 0.5f, h * 0.5f, w * 0.36f, paint);
                c.drawLine(w * 0.5f, h * 0.29f, w * 0.5f, h * 0.5f, paint);
                c.drawLine(w * 0.5f, h * 0.5f, w * 0.66f, h * 0.60f, paint);
            } else if (kind == SEND) {
                p.moveTo(w * 0.86f, h * 0.14f);
                p.lineTo(w * 0.58f, h * 0.86f);
                p.lineTo(w * 0.44f, h * 0.56f);
                p.lineTo(w * 0.14f, h * 0.42f);
                p.close();
                c.drawPath(p, paint);
                c.drawLine(w * 0.86f, h * 0.14f, w * 0.44f, h * 0.56f, paint);
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
        } else if ("punch".equalsIgnoreCase(via)
                || "cancelled".equalsIgnoreCase(via)
                || "peer_cancelled".equalsIgnoreCase(via)) {
            t.setTextColor(cWarn);
            t.setBackground(roundBox(cWarnContainer, 0, 99));
        } else if ("fail".equalsIgnoreCase(via) || "failed".equalsIgnoreCase(via)) {
            t.setTextColor(cOnErrorContainer);
            t.setBackground(roundBox(cErrorContainer, 0, 99));
        } else {
            t.setTextColor(cOnSurface);
            t.setBackground(roundBox(cBorder, 0, 99));
        }
    }

    private static String pathLabel(String path) {
        if ("lan".equalsIgnoreCase(path)) {
            return "LAN 直连";
        }
        if ("punch".equalsIgnoreCase(path)) {
            return "UDP 打洞";
        }
        if ("relay".equalsIgnoreCase(path)) {
            return "服务器中继";
        }
        if ("web".equalsIgnoreCase(path)) {
            return "网页快传";
        }
        if ("cancelled".equalsIgnoreCase(path)) {
            return "已取消";
        }
        if ("peer_cancelled".equalsIgnoreCase(path)) {
            return "对方已取消";
        }
        if ("fail".equalsIgnoreCase(path) || "failed".equalsIgnoreCase(path)) {
            return "失败";
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
        Intent i = new Intent(this, TransferService.class)
                .setAction(TransferService.ACTION_UPDATE_TEXT)
                .putExtra("text", text);
        if (Build.VERSION.SDK_INT >= 26) {
            startForegroundService(i);
        } else {
            startService(i);
        }
    }

    private void stopKeepAlive() {
        if (!TransferService.isRecvRunning()) {
            stopService(new Intent(this, TransferService.class));
        }
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

    private LinearLayout settingKeepAliveNotifRow() {
        setKeepAliveNotifBtn = smallOutlineBtn("去关闭", v -> showKeepAliveNotifGuide());
        LinearLayout row = settingActionRow(
                "保活状态通知",
                "默认最低优先级；如需彻底隐藏，可引导关闭此通知渠道",
                setKeepAliveNotifBtn,
                v -> showKeepAliveNotifGuide());
        setKeepAliveNotifSub = (TextView) ((LinearLayout) row.getChildAt(0)).getChildAt(1);
        return row;
    }

    private void styleTogglePill(Button btn, boolean active, String activeText, String inactiveText) {
        if (btn == null) {
            return;
        }
        btn.setText(active ? activeText : inactiveText);
        btn.setBackground(ripple(roundBox(
                active ? cPrimaryContainer : cSurface,
                active ? cPrimary : cBorder, 10), 10));
        btn.setTextColor(active ? cOnPrimaryContainer : cOnSurfaceVariant);
    }

    private void refreshKeepAliveNotifUi() {
        boolean muted = TransferService.isStatusChannelMuted(this);
        if (setKeepAliveNotifSub != null) {
            setKeepAliveNotifSub.setText(muted
                    ? "已关闭保活通知，不影响「收到新文件通知」"
                    : "默认最低优先级；如需彻底隐藏，可引导关闭此通知渠道");
        }
        styleTogglePill(setKeepAliveNotifBtn, muted, "已关闭", "去关闭");
    }

    private void showKeepAliveNotifGuide() {
        TransferService.ensureChannels(this);
        String msg = Build.VERSION.SDK_INT >= 26
                ? "轻渡已默认将「后台接收服务」通知设为最低优先级，以减少状态栏打扰。\n\n如需彻底隐藏状态栏图标和常驻横幅，可在即将打开的通知设置页中关闭「后台接收服务」开关；这不会影响「收到新文件通知」，后台仍可正常接收文件。"
                : "轻渡已默认将后台接收通知设为最低优先级。\n\n如需彻底隐藏，可在应用信息页关闭通知显示；但系统版本较旧时，全部关闭通知可能一并屏蔽新文件提醒。";
        new AlertDialog.Builder(this)
                .setTitle("关闭保活状态通知")
                .setMessage(msg)
                .setNegativeButton("取消", null)
                .setPositiveButton("前往通知设置", (d, w) -> openKeepAliveChannelSettings())
                .show();
    }

    private void openKeepAliveChannelSettings() {
        TransferService.ensureChannels(this);
        if (Build.VERSION.SDK_INT >= 26) {
            try {
                Intent i = new Intent(Settings.ACTION_CHANNEL_NOTIFICATION_SETTINGS);
                i.putExtra(Settings.EXTRA_APP_PACKAGE, getPackageName());
                i.putExtra(Settings.EXTRA_CHANNEL_ID, TransferService.CHANNEL_STATUS);
                startActivity(i);
                return;
            } catch (Exception ignored) {
            }
            try {
                Intent i = new Intent(Settings.ACTION_APP_NOTIFICATION_SETTINGS);
                i.putExtra(Settings.EXTRA_APP_PACKAGE, getPackageName());
                startActivity(i);
                return;
            } catch (Exception ignored) {
            }
        }
        try {
            Intent i = new Intent(Settings.ACTION_APPLICATION_DETAILS_SETTINGS);
            i.setData(Uri.parse("package:" + getPackageName()));
            startActivity(i);
        } catch (Exception e) {
            toast("请在系统设置 → 应用通知中关闭「后台接收服务」");
        }
    }

    private boolean isHideRecentsEnabled() {
        return getPreferences(MODE_PRIVATE).getBoolean("hideRecents", false);
    }

    private void setHideRecentsEnabled(boolean hide) {
        getPreferences(MODE_PRIVATE).edit().putBoolean("hideRecents", hide).apply();
        applyExcludeFromRecents(hide);
        refreshHideRecentsUi();
    }

    private void applyExcludeFromRecents(boolean hide) {
        if (Build.VERSION.SDK_INT < 21) {
            return;
        }
        try {
            ActivityManager am = (ActivityManager) getSystemService(ACTIVITY_SERVICE);
            if (am == null) {
                return;
            }
            List<ActivityManager.AppTask> tasks = am.getAppTasks();
            if (tasks != null) {
                for (ActivityManager.AppTask task : tasks) {
                    if (task != null) {
                        task.setExcludeFromRecents(hide);
                    }
                }
            }
        } catch (Exception e) {
            Log.w(TAG, "applyExcludeFromRecents", e);
        }
    }

    private void refreshHideRecentsUi() {
        styleTogglePill(setHideRecentsBtn, isHideRecentsEnabled(), "已开启", "已关闭");
    }

    private void onHideRecentsToggleClicked() {
        if (isHideRecentsEnabled()) {
            setHideRecentsEnabled(false);
            toast("已关闭隐藏后台，卡片将重新显示在最近任务中");
        } else {
            showHideRecentsGuideDialog();
        }
    }

    private void showHideRecentsGuideDialog() {
        new AlertDialog.Builder(this)
                .setTitle("开启前请先锁定后台卡片")
                .setMessage("开启「隐藏最近任务后台」后，轻渡将不再出现在手机多任务列表中，隐藏后将无法再通过多任务卡片加锁。\n\n请先按以下步骤锁定后台，防止被系统清理：\n1. 上滑进入手机最近任务界面；\n2. 长按「轻渡」卡片并点击「锁定 / 加锁」；\n3. 返回轻渡后，点击下方「已锁定，开启隐藏」。")
                .setNegativeButton("暂不开启", null)
                .setPositiveButton("已锁定，开启隐藏", (d, w) -> {
                    setHideRecentsEnabled(true);
                    toast("已开启隐藏最近任务后台");
                })
                .show();
    }

    private String lanHint() {
        return TransferService.lanHint(this);
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

    private long displaySize(Uri uri) {
        try (Cursor c = getContentResolver().query(uri, null, null, null, null)) {
            if (c != null && c.moveToFirst()) {
                int idx = c.getColumnIndex(OpenableColumns.SIZE);
                if (idx >= 0 && !c.isNull(idx)) {
                    return c.getLong(idx);
                }
            }
        } catch (Exception e) {
            Log.w(TAG, "query size", e);
        }
        return -1;
    }

    private static void deleteRecursive(File f) {
        if (f == null || !f.exists()) {
            return;
        }
        if (f.isDirectory()) {
            File[] children = f.listFiles();
            if (children != null) {
                for (File c : children) {
                    deleteRecursive(c);
                }
            }
        }
        f.delete();
    }

    private static void deleteTree(File f, File dir) {
        deleteRecursive(f);
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

    private static String fmtSpeed(long bps) {
        if (bps <= 0L) {
            return "";
        }
        if (bps < 1024L) {
            return bps + " B/s";
        }
        if (bps < 1048576L) {
            return String.format(Locale.US, "%.1f KB/s", bps / 1024.0);
        }
        if (bps < 1073741824L) {
            return String.format(Locale.US, "%.1f MB/s", bps / 1048576.0);
        }
        return String.format(Locale.US, "%.2f GB/s", bps / 1073741824.0);
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
