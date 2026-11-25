package cn.cwlindean.ft;

import android.app.Notification;
import android.app.NotificationChannel;
import android.app.NotificationManager;
import android.app.PendingIntent;
import android.app.Service;
import android.content.BroadcastReceiver;
import android.content.ContentValues;
import android.content.Context;
import android.content.Intent;
import android.content.IntentFilter;
import android.content.SharedPreferences;
import android.content.UriPermission;
import android.content.pm.ServiceInfo;
import android.net.ConnectivityManager;
import android.net.LinkProperties;
import android.net.Network;
import android.net.NetworkCapabilities;
import android.net.NetworkRequest;
import android.net.Uri;
import android.net.wifi.WifiManager;
import android.os.Build;
import android.os.Environment;
import android.os.IBinder;
import android.os.PowerManager;
import android.provider.DocumentsContract;
import android.provider.MediaStore;
import android.util.Log;
import android.webkit.MimeTypeMap;

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
import java.util.ArrayList;
import java.util.Collections;
import java.util.List;
import java.util.Locale;
import java.util.concurrent.ExecutorService;
import java.util.concurrent.Executors;

import mobile.EventListener;
import mobile.Mobile;

/**
 * Long-lived receive kernel on Android:
 * - Owns the Go auto-receiver, LAN TCP listener, file export, and received notifications.
 * - Suspends idle signaling and LAN broadcasts while the screen is locked (zero wakeups),
 *   and reconnects immediately when the screen wakes or network changes.
 * - Acquires bounded WakeLock/WifiLock only while an active transfer is running.
 */
public class TransferService extends Service implements EventListener {
    private static final String TAG = "FerryService";
    static final String CHANNEL_STATUS = "ft-transfer";
    static final String CHANNEL_RECEIVED = "ft-received";
    static final int NOTIFICATION_ID = 1;
    private static final int RECV_NOTIF_BASE = 200;

    static final String ACTION_START_RECV = "cn.cwlindean.ft.ACTION_START_RECV";
    static final String ACTION_STOP_RECV = "cn.cwlindean.ft.ACTION_STOP_RECV";
    static final String ACTION_UPDATE_TEXT = "cn.cwlindean.ft.ACTION_UPDATE_TEXT";
    static final String ACTION_SWEEP_INBOX = "cn.cwlindean.ft.ACTION_SWEEP_INBOX";

    static final String SIGNAL_URL = "wss://ferry.cwlindean.cn/signal";

    public interface UiBridge {
        void onServiceStateChanged(boolean running, String lanPort, String error);
        void onConnectionSnapshot(String connJson);
        void onTransferProgressSnapshot(String progressJson);
        void onFilesReceived(List<JSONObject> entries, boolean refreshPeers);
        void onConfirmIncoming(String reqJson);
    }

    private static final Object BRIDGE_LOCK = new Object();
    private static final Object RECENT_LOCK = new Object();
    private static volatile UiBridge sUiBridge;
    private static volatile boolean sRecvRunning;
    private static volatile String sLanPort = "";
    private static int sNotifSeq = 1;

    private ExecutorService worker;
    private PowerManager.WakeLock transferWakeLock;
    private WifiManager.WifiLock transferWifiLock;
    private BroadcastReceiver screenReceiver;
    private ConnectivityManager.NetworkCallback networkCallback;
    private volatile boolean screenAwake = true;
    private volatile boolean transferActive = false;
    private volatile boolean webShareActive = false;
    private volatile String currentSignalState = "offline";
    private String lastNotifText = "";

    public static void setUiBridge(UiBridge bridge) {
        synchronized (BRIDGE_LOCK) {
            sUiBridge = bridge;
        }
        try {
            Mobile.setMainUIOpen(bridge != null);
        } catch (Throwable ignored) {
        }
    }

    public static void clearUiBridge(UiBridge bridge) {
        boolean cleared = false;
        synchronized (BRIDGE_LOCK) {
            if (sUiBridge == bridge) {
                sUiBridge = null;
                cleared = true;
            }
        }
        if (cleared) {
            try {
                Mobile.setMainUIOpen(false);
            } catch (Throwable ignored) {
            }
        }
    }

    public static boolean isRecvRunning() {
        return sRecvRunning;
    }

    public static String getLanPort() {
        return sLanPort;
    }

    public static SharedPreferences prefs(Context ctx) {
        return ctx.getApplicationContext().getSharedPreferences("MainActivity", MODE_PRIVATE);
    }

    public static File stateDir(Context ctx) {
        File d = new File(ctx.getApplicationContext().getFilesDir(), "state");
        if (!d.exists()) {
            d.mkdirs();
        }
        return d;
    }

    public static File inboxDir(Context ctx) {
        File d = new File(ctx.getApplicationContext().getFilesDir(), "inbox");
        if (!d.exists()) {
            d.mkdirs();
        }
        return d;
    }

    public static File keyFile(Context ctx) {
        return new File(stateDir(ctx), "device.key");
    }

    public static File pairedFile(Context ctx) {
        return new File(stateDir(ctx), "paired.json");
    }

    public static File recentFile(Context ctx) {
        return new File(stateDir(ctx), "recent.json");
    }

    public static String loadDeviceName(Context ctx) {
        String saved = prefs(ctx).getString("deviceName", "").trim();
        if (!saved.isEmpty()) {
            return saved;
        }
        String model = Build.MODEL != null ? Build.MODEL.trim() : "";
        return model.isEmpty() ? "Android" : model;
    }

    @Override
    public void onCreate() {
        super.onCreate();
        worker = Executors.newSingleThreadExecutor();
        createChannels();
        try {
            PowerManager pm = (PowerManager) getSystemService(POWER_SERVICE);
            if (pm != null) {
                screenAwake = pm.isInteractive();
            }
        } catch (Exception ignored) {
        }
        registerScreenReceiver();
        registerNetCallback();
        Mobile.setEventListener(this);
        Mobile.setScreenAwake(screenAwake);
        promoteForeground("轻渡服务启动中…");
    }

    @Override
    public int onStartCommand(Intent intent, int flags, int startId) {
        String action = intent != null ? intent.getAction() : null;
        if (ACTION_STOP_RECV.equals(action)) {
            stopReceiveLocked();
            return START_NOT_STICKY;
        }
        if (ACTION_UPDATE_TEXT.equals(action)) {
            String txt = intent.getStringExtra("text");
            if (txt != null && !txt.isEmpty()) {
                webShareActive = txt.contains("临时快传");
                updateStatusNotification(txt);
            }
            return sRecvRunning ? START_STICKY : START_NOT_STICKY;
        }
        if (ACTION_SWEEP_INBOX.equals(action)) {
            worker.execute(this::drainAndSweep);
            return sRecvRunning ? START_STICKY : START_NOT_STICKY;
        }

        // ACTION_START_RECV or null (START_STICKY system restart)
        boolean wantAuto = prefs(this).getBoolean("recvAuto", true);
        if (ACTION_START_RECV.equals(action) || wantAuto) {
            startReceiveAsync();
            return START_STICKY;
        }
        stopSelf();
        return START_NOT_STICKY;
    }

    @Override
    public void onDestroy() {
        Mobile.setEventListener(null);
        unregisterScreenReceiver();
        unregisterNetCallback();
        releaseTransferLocks();
        if (worker != null) {
            worker.shutdownNow();
        }
        super.onDestroy();
    }

    @Override
    public IBinder onBind(Intent intent) {
        return null;
    }

    private void startReceiveAsync() {
        worker.execute(() -> {
            String name = loadDeviceName(this);
            String hint = lanHint(this);
            String lanAddr = "";
            String errMsg = "";
            if (screenAwake) {
                try {
                    lanAddr = Mobile.startReceiverWithAuth(name, inboxDir(this).getAbsolutePath(),
                            keyFile(this).getAbsolutePath(), pairedFile(this).getAbsolutePath(), hint);
                } catch (Exception e) {
                    Log.w(TAG, "startReceiverWithAuth", e);
                }
            }
            try {
                Mobile.setScreenAwake(screenAwake);
                Mobile.startAutoReceive(name, inboxDir(this).getAbsolutePath(), SIGNAL_URL,
                        keyFile(this).getAbsolutePath(), pairedFile(this).getAbsolutePath(), hint, "");
            } catch (Exception e) {
                Log.e(TAG, "startAutoReceive", e);
                errMsg = e.getMessage() != null ? e.getMessage() : "启动失败";
            }

            boolean ok = !lanAddr.isEmpty() || errMsg.isEmpty();
            sRecvRunning = ok;
            sLanPort = extractPort(lanAddr);
            if (ok) {
                if (!screenAwake) {
                    updateStatusNotification("已锁屏暂停接收（亮屏自动恢复）");
                } else {
                    updateStatusNotification(sLanPort.isEmpty() ? "正在等待接收文件" : "待命中 · 端口 " + sLanPort);
                }
                drainAndSweep();
            } else {
                updateStatusNotification("接收启动失败：" + errMsg);
            }
            notifyUiServiceState(errMsg);
        });
    }

    private void stopReceiveLocked() {
        worker.execute(() -> {
            try {
                Mobile.stopReceiver();
                Mobile.stopAutoReceive();
                Mobile.stopRelayReceiver();
                Mobile.stopPunchReceiver();
            } catch (Exception e) {
                Log.w(TAG, "stopReceive", e);
            }
            drainAndSweep();
            sRecvRunning = false;
            sLanPort = "";
            currentSignalState = "offline";
            releaseTransferLocks();
            notifyUiServiceState("");
            if (!webShareActive) {
                stopForeground(true);
                stopSelf();
            }
        });
    }

    // ================= Go EventListener Callbacks =================

    @Override
    public void onReceived(String eventJSON) {
        if (worker != null && !worker.isShutdown()) {
            worker.execute(this::drainAndSweep);
        }
    }

    @Override
    public void onTransferProgress(String statusJSON) {
        boolean active = false;
        String stageText = "";
        try {
            JSONObject o = new JSONObject(statusJSON);
            active = o.optBoolean("active", false);
            if (active) {
                String dir = "recv".equals(o.optString("direction", "recv")) ? "接收" : "发送";
                String name = o.optString("file_name", "");
                long done = o.optLong("bytes_done", 0);
                long total = o.optLong("bytes_total", 0);
                if (total > 0) {
                    int pct = (int) Math.max(0, Math.min(100, (done * 100L) / total));
                    stageText = "正在" + dir + " " + pct + "%" + (name.isEmpty() ? "" : " · " + name);
                } else {
                    stageText = "正在" + dir + (name.isEmpty() ? "…" : " · " + name);
                }
            }
        } catch (Exception ignored) {
        }

        boolean wasActive = transferActive;
        transferActive = active;
        if (active && !wasActive) {
            acquireTransferLocks();
        } else if (!active && wasActive) {
            releaseTransferLocks();
            if (!screenAwake && sRecvRunning && worker != null && !worker.isShutdown()) {
                worker.execute(() -> {
                    try {
                        Mobile.stopReceiver();
                    } catch (Exception ignored) {
                    }
                    sLanPort = "";
                    updateStatusNotification("已锁屏暂停接收（亮屏自动恢复）");
                });
            } else {
                refreshIdleNotif();
            }
        }
        if (active && !stageText.isEmpty()) {
            updateStatusNotification(stageText);
        }

        UiBridge b = sUiBridge;
        if (b != null) {
            b.onTransferProgressSnapshot(statusJSON);
        }
    }

    @Override
    public void onConnectionChanged(String statusJSON) {
        try {
            JSONObject o = new JSONObject(statusJSON);
            currentSignalState = o.optString("signal_state", "offline");
        } catch (Exception ignored) {
        }
        if (!transferActive && sRecvRunning) {
            refreshIdleNotif();
        }
        UiBridge b = sUiBridge;
        if (b != null) {
            b.onConnectionSnapshot(statusJSON);
        }
    }

    @Override
    public void onConfirmIncoming(String reqJSON) {
        UiBridge b = sUiBridge;
        if (b != null) {
            b.onConfirmIncoming(reqJSON);
            return;
        }
        try {
            String id = new JSONObject(reqJSON).optString("id", "");
            if (!id.isEmpty()) {
                Mobile.respondIncoming(id, false);
            }
        } catch (Exception ignored) {
        }
    }

    private void refreshIdleNotif() {
        if (!sRecvRunning) {
            return;
        }
        if (!screenAwake) {
            updateStatusNotification("已锁屏暂停接收（亮屏自动恢复）");
            return;
        }
        if ("online".equals(currentSignalState)) {
            updateStatusNotification(sLanPort.isEmpty() ? "已在线 · 等待接收" : "已在线 · 端口 " + sLanPort);
        } else if ("connecting".equals(currentSignalState)) {
            updateStatusNotification(sLanPort.isEmpty() ? "信令重连中…" : "局域网待命 · 端口 " + sLanPort);
        } else {
            updateStatusNotification(sLanPort.isEmpty() ? "等待网络连接" : "局域网待命 · 端口 " + sLanPort);
        }
    }

    private void drainAndSweep() {
        List<JSONObject> exported = new ArrayList<>();
        boolean refreshPeers = false;
        try {
            String raw = Mobile.pollReceived();
            if (raw != null && !raw.equals("[]") && !raw.trim().isEmpty()) {
                JSONArray arr = new JSONArray(raw);
                for (int i = 0; i < arr.length(); i++) {
                    JSONObject o = arr.getJSONObject(i);
                    String fileName = o.optString("file", "");
                    String fullPath = o.optString("full_path", "");
                    String via = o.optString("via", "");
                    String peer = o.optString("peer", "");
                    long bytes = o.optLong("bytes", 0);
                    long ts = o.optLong("ts", System.currentTimeMillis());
                    String status = o.optString("status", "");
                    String errMsg = o.optString("error", "");
                    if (!status.isEmpty() && !"ok".equals(status)) {
                        if (fileName.isEmpty()) {
                            fileName = "接收文件";
                        }
                        JSONObject rec = makeRecentJson(fileName, "", "", via, bytes, ts, "recv", peer, status, errMsg);
                        appendRecentRecord(this, rec);
                        exported.add(rec);
                        continue;
                    }
                    if (fileName.isEmpty() || fullPath.isEmpty()) {
                        continue;
                    }
                    File src = new File(fullPath);
                    if (!src.isFile()) {
                        continue;
                    }
                    if (!peer.isEmpty()) {
                        refreshPeers = true;
                    }
                    String[] saved = exportStagedFile(this, src, fileName);
                    if (saved != null) {
                        JSONObject rec = makeRecentJson(fileName, saved[0], guessMime(fileName), via, bytes, ts, "recv", peer);
                        appendRecentRecord(this, rec);
                        exported.add(rec);
                    }
                }
            }
        } catch (Exception e) {
            Log.w(TAG, "drainReceived", e);
        }

        // Sweep any leftover files staged before crash/restart.
        File[] files = inboxDir(this).listFiles();
        if (files != null) {
            for (File f : files) {
                if (f.isFile() && !f.getName().endsWith(".ftpart")) {
                    String[] saved = exportStagedFile(this, f, f.getName());
                    if (saved != null) {
                        JSONObject rec = makeRecentJson(f.getName(), saved[0], guessMime(f.getName()), "", f.length(), f.lastModified(), "recv", "");
                        appendRecentRecord(this, rec);
                        exported.add(rec);
                    }
                }
            }
        }

        if (!exported.isEmpty()) {
            UiBridge b = sUiBridge;
            if (b != null) {
                b.onFilesReceived(exported, refreshPeers);
            } else {
                postReceivedNotification(exported);
            }
        }
    }

    // ================= Screen & Network Listeners =================

    private void registerScreenReceiver() {
        IntentFilter f = new IntentFilter();
        f.addAction(Intent.ACTION_SCREEN_OFF);
        f.addAction(Intent.ACTION_SCREEN_ON);
        f.addAction(Intent.ACTION_USER_PRESENT);
        screenReceiver = new BroadcastReceiver() {
            @Override
            public void onReceive(Context context, Intent intent) {
                String act = intent != null ? intent.getAction() : "";
                if (Intent.ACTION_SCREEN_OFF.equals(act)) {
                    screenAwake = false;
                    Mobile.setScreenAwake(false);
                    if (!transferActive && sRecvRunning && worker != null && !worker.isShutdown()) {
                        worker.execute(() -> {
                            try {
                                Mobile.stopReceiver();
                            } catch (Exception ignored) {
                            }
                            sLanPort = "";
                            updateStatusNotification("已锁屏暂停接收（亮屏自动恢复）");
                            notifyUiServiceState("");
                        });
                    }
                } else if (Intent.ACTION_SCREEN_ON.equals(act) || Intent.ACTION_USER_PRESENT.equals(act)) {
                    boolean wasAsleep = !screenAwake;
                    screenAwake = true;
                    if (sRecvRunning && worker != null && !worker.isShutdown()) {
                        worker.execute(() -> {
                            String hint = lanHint(TransferService.this);
                            Mobile.updateNetworkHint(hint);
                            if (wasAsleep || sLanPort.isEmpty()) {
                                try {
                                    String addr = Mobile.startReceiverWithAuth(loadDeviceName(TransferService.this),
                                            inboxDir(TransferService.this).getAbsolutePath(),
                                            keyFile(TransferService.this).getAbsolutePath(),
                                            pairedFile(TransferService.this).getAbsolutePath(), hint);
                                    sLanPort = extractPort(addr);
                                } catch (Exception ignored) {
                                }
                            }
                            Mobile.setScreenAwake(true);
                            Mobile.reconnectAutoReceive();
                            refreshIdleNotif();
                            notifyUiServiceState("");
                        });
                    } else {
                        Mobile.setScreenAwake(true);
                    }
                }
            }
        };
        if (Build.VERSION.SDK_INT >= 33) {
            registerReceiver(screenReceiver, f, Context.RECEIVER_NOT_EXPORTED);
        } else {
            registerReceiver(screenReceiver, f);
        }
    }

    private void unregisterScreenReceiver() {
        if (screenReceiver != null) {
            try {
                unregisterReceiver(screenReceiver);
            } catch (Exception ignored) {
            }
            screenReceiver = null;
        }
    }

    private void registerNetCallback() {
        try {
            ConnectivityManager cm = (ConnectivityManager) getSystemService(CONNECTIVITY_SERVICE);
            if (cm == null) {
                return;
            }
            networkCallback = new ConnectivityManager.NetworkCallback() {
                @Override
                public void onAvailable(Network network) {
                    onNetworkChanged(true);
                }

                @Override
                public void onCapabilitiesChanged(Network network, NetworkCapabilities caps) {
                    onNetworkChanged(true);
                }

                @Override
                public void onLinkPropertiesChanged(Network network, LinkProperties lp) {
                    onNetworkChanged(true);
                }

                @Override
                public void onLost(Network network) {
                    boolean hasAny = false;
                    try {
                        if (Build.VERSION.SDK_INT >= 23) {
                            Network active = cm.getActiveNetwork();
                            hasAny = active != null;
                        } else {
                            hasAny = cm.getActiveNetworkInfo() != null && cm.getActiveNetworkInfo().isConnected();
                        }
                    } catch (Exception ignored) {
                    }
                    onNetworkChanged(hasAny);
                }
            };
            if (Build.VERSION.SDK_INT >= 24) {
                cm.registerDefaultNetworkCallback(networkCallback);
            } else {
                cm.registerNetworkCallback(new NetworkRequest.Builder()
                        .addCapability(NetworkCapabilities.NET_CAPABILITY_INTERNET)
                        .build(), networkCallback);
            }
        } catch (Exception e) {
            Log.w(TAG, "registerNetCallback", e);
        }
    }

    private void onNetworkChanged(boolean available) {
        if (worker == null || worker.isShutdown()) {
            return;
        }
        worker.execute(() -> {
            String hint = available ? lanHint(TransferService.this) : "";
            Mobile.updateNetworkHint(hint);
            Mobile.setNetworkAvailable(available);
            if (available && screenAwake && sRecvRunning) {
                Mobile.reconnectAutoReceive();
            }
        });
    }

    private void unregisterNetCallback() {
        if (networkCallback != null) {
            try {
                ConnectivityManager cm = (ConnectivityManager) getSystemService(CONNECTIVITY_SERVICE);
                if (cm != null) {
                    cm.unregisterNetworkCallback(networkCallback);
                }
            } catch (Exception ignored) {
            }
            networkCallback = null;
        }
    }

    // ================= Power Locks (Active Transfer Only) =================

    private void acquireTransferLocks() {
        try {
            PowerManager pm = (PowerManager) getSystemService(POWER_SERVICE);
            if (pm != null && transferWakeLock == null) {
                transferWakeLock = pm.newWakeLock(PowerManager.PARTIAL_WAKE_LOCK, "ferry:transfer");
                transferWakeLock.setReferenceCounted(false);
            }
            if (transferWakeLock != null && !transferWakeLock.isHeld()) {
                transferWakeLock.acquire(15 * 60 * 1000L);
            }
        } catch (Exception e) {
            Log.w(TAG, "wakeLock", e);
        }
        try {
            WifiManager wm = (WifiManager) getApplicationContext().getSystemService(WIFI_SERVICE);
            if (wm != null && transferWifiLock == null) {
                transferWifiLock = wm.createWifiLock(WifiManager.WIFI_MODE_FULL_HIGH_PERF, "ferry:wifi");
                transferWifiLock.setReferenceCounted(false);
            }
            if (transferWifiLock != null && !transferWifiLock.isHeld()) {
                transferWifiLock.acquire();
            }
        } catch (Exception e) {
            Log.w(TAG, "wifiLock", e);
        }
    }

    private void releaseTransferLocks() {
        try {
            if (transferWakeLock != null && transferWakeLock.isHeld()) {
                transferWakeLock.release();
            }
        } catch (Exception ignored) {
        }
        try {
            if (transferWifiLock != null && transferWifiLock.isHeld()) {
                transferWifiLock.release();
            }
        } catch (Exception ignored) {
        }
    }

    // ================= Notifications =================

    private void createChannels() {
        if (Build.VERSION.SDK_INT < 26) {
            return;
        }
        NotificationManager nm = getSystemService(NotificationManager.class);
        if (nm == null) {
            return;
        }
        NotificationChannel statusCh = new NotificationChannel(
                CHANNEL_STATUS, "后台接收服务", NotificationManager.IMPORTANCE_LOW);
        statusCh.setShowBadge(false);
        nm.createNotificationChannel(statusCh);

        NotificationChannel recvCh = new NotificationChannel(
                CHANNEL_RECEIVED, "收到新文件通知", NotificationManager.IMPORTANCE_DEFAULT);
        nm.createNotificationChannel(recvCh);
    }

    private PendingIntent openAppIntent() {
        Intent open = new Intent(this, MainActivity.class);
        open.addFlags(Intent.FLAG_ACTIVITY_SINGLE_TOP | Intent.FLAG_ACTIVITY_CLEAR_TOP);
        int flags = PendingIntent.FLAG_UPDATE_CURRENT;
        if (Build.VERSION.SDK_INT >= 23) {
            flags |= PendingIntent.FLAG_IMMUTABLE;
        }
        return PendingIntent.getActivity(this, 0, open, flags);
    }

    private Notification buildStatusNotification(String text) {
        Notification.Builder b = Build.VERSION.SDK_INT >= 26
                ? new Notification.Builder(this, CHANNEL_STATUS)
                : new Notification.Builder(this);
        b.setContentTitle("轻渡 Ferry")
                .setContentText(text)
                .setSmallIcon(android.R.drawable.stat_sys_upload)
                .setContentIntent(openAppIntent())
                .setOngoing(true);
        return b.build();
    }

    private void promoteForeground(String text) {
        lastNotifText = text;
        Notification n = buildStatusNotification(text);
        if (Build.VERSION.SDK_INT >= 34) {
            startForeground(NOTIFICATION_ID, n, ServiceInfo.FOREGROUND_SERVICE_TYPE_SPECIAL_USE);
        } else if (Build.VERSION.SDK_INT >= 29) {
            startForeground(NOTIFICATION_ID, n, ServiceInfo.FOREGROUND_SERVICE_TYPE_DATA_SYNC);
        } else {
            startForeground(NOTIFICATION_ID, n);
        }
    }

    private void updateStatusNotification(String text) {
        if (text == null || text.equals(lastNotifText)) {
            return;
        }
        lastNotifText = text;
        NotificationManager nm = (NotificationManager) getSystemService(NOTIFICATION_SERVICE);
        if (nm != null) {
            nm.notify(NOTIFICATION_ID, buildStatusNotification(text));
        }
    }

    private void postReceivedNotification(List<JSONObject> entries) {
        NotificationManager nm = (NotificationManager) getSystemService(NOTIFICATION_SERVICE);
        if (nm == null || entries.isEmpty()) {
            return;
        }
        List<JSONObject> okFiles = new ArrayList<>();
        List<JSONObject> outcomes = new ArrayList<>();
        for (JSONObject o : entries) {
            String st = o.optString("status", "");
            if (st.isEmpty() || "ok".equals(st)) {
                okFiles.add(o);
            } else {
                outcomes.add(o);
            }
        }
        if (!okFiles.isEmpty()) {
            String first = okFiles.get(0).optString("name", "文件");
            String peer = okFiles.get(0).optString("peer", "");
            String title = okFiles.size() > 1 ? ("已收到 " + okFiles.size() + " 个文件") : ("已收到文件：" + first);
            String body = peer.isEmpty() ? "已存入下载目录" : ("来自 " + peer + "，已存入下载目录");

            Notification.Builder b = Build.VERSION.SDK_INT >= 26
                    ? new Notification.Builder(this, CHANNEL_RECEIVED)
                    : new Notification.Builder(this);
            b.setContentTitle(title)
                    .setContentText(body)
                    .setSmallIcon(android.R.drawable.stat_sys_download_done)
                    .setAutoCancel(true)
                    .setContentIntent(openAppIntent());
            nm.notify(RECV_NOTIF_BASE + (sNotifSeq++ % 20), b.build());
        }
        if (!outcomes.isEmpty()) {
            JSONObject firstOut = outcomes.get(0);
            String st = firstOut.optString("status", "");
            String name = firstOut.optString("name", "文件");
            String peer = firstOut.optString("peer", "");
            String errMsg = firstOut.optString("error", "");
            String title;
            String body;
            int icon = android.R.drawable.stat_notify_error;
            if ("peer_cancelled".equals(st)) {
                title = "传输已取消";
                body = peer.isEmpty() ? ("对方已取消发送：" + name) : ("对方（" + peer + "）已取消发送：" + name);
                icon = android.R.drawable.stat_sys_warning;
            } else if ("cancelled".equals(st)) {
                title = "传输已取消";
                body = "已取消接收：" + name;
                icon = android.R.drawable.stat_sys_warning;
            } else {
                title = "接收失败";
                body = !errMsg.isEmpty() ? errMsg : ("接收失败：" + name);
            }
            Notification.Builder b = Build.VERSION.SDK_INT >= 26
                    ? new Notification.Builder(this, CHANNEL_RECEIVED)
                    : new Notification.Builder(this);
            b.setContentTitle(title)
                    .setContentText(body)
                    .setSmallIcon(icon)
                    .setAutoCancel(true)
                    .setContentIntent(openAppIntent());
            nm.notify(RECV_NOTIF_BASE + (sNotifSeq++ % 20), b.build());
        }
    }

    private void notifyUiServiceState(String error) {
        UiBridge b = sUiBridge;
        if (b != null) {
            b.onServiceStateChanged(sRecvRunning, sLanPort, error);
        }
    }

    // ================= Shared Storage, Export & Network Helpers =================

    public static JSONObject makeRecentJson(String name, String uri, String mime, String via, long bytes, long ts, String dir, String peer) {
        return makeRecentJson(name, uri, mime, via, bytes, ts, dir, peer, "", "");
    }

    public static JSONObject makeRecentJson(String name, String uri, String mime, String via, long bytes, long ts, String dir, String peer, String status, String error) {
        JSONObject o = new JSONObject();
        try {
            o.put("name", name);
            o.put("uri", uri);
            o.put("mime", mime);
            o.put("via", via);
            o.put("bytes", bytes);
            o.put("ts", ts);
            o.put("dir", dir);
            o.put("peer", peer);
            if (status != null && !status.isEmpty()) {
                o.put("status", status);
            }
            if (error != null && !error.isEmpty()) {
                o.put("error", error);
            }
        } catch (Exception ignored) {
        }
        return o;
    }

    public static void appendRecentRecord(Context ctx, JSONObject rec) {
        synchronized (RECENT_LOCK) {
            try {
                JSONArray next = new JSONArray();
                next.put(rec);
                File rf = recentFile(ctx);
                if (rf.isFile()) {
                    try (InputStream in = new FileInputStream(rf)) {
                        ByteArrayOutputStream bos = new ByteArrayOutputStream();
                        copyStream(in, bos);
                        JSONArray old = new JSONArray(bos.toString("UTF-8"));
                        for (int i = 0; i < old.length() && next.length() < 30; i++) {
                            next.put(old.get(i));
                        }
                    } catch (Exception ignored) {
                    }
                }
                try (OutputStream out = new FileOutputStream(rf)) {
                    out.write(next.toString().getBytes("UTF-8"));
                }
            } catch (Exception e) {
                Log.w(TAG, "appendRecentRecord", e);
            }
        }
    }

    public static void saveRecentList(Context ctx, JSONArray arr) {
        synchronized (RECENT_LOCK) {
            try (OutputStream out = new FileOutputStream(recentFile(ctx))) {
                out.write(arr.toString().getBytes("UTF-8"));
            } catch (Exception e) {
                Log.w(TAG, "saveRecentList", e);
            }
        }
    }

    public static String[] exportStagedFile(Context ctx, File src, String fileName) {
        String mime = guessMime(fileName);
        SharedPreferences sp = prefs(ctx);
        String customUri = sp.getString("customDownloadTreeUri", "").trim();
        String customLbl = sp.getString("customDownloadLabel", "").trim();
        String savedUri = null;
        String destLabel = "下载";

        if (!customUri.isEmpty()) {
            try {
                Uri tree = Uri.parse(customUri);
                if (hasPersistedTreePermission(ctx, tree)) {
                    savedUri = saveToCustomTree(ctx, tree, src, fileName, mime);
                    if (savedUri != null) {
                        destLabel = customLbl.isEmpty() ? "自定义文件夹" : customLbl;
                    }
                }
            } catch (Exception ignored) {
            }
        }
        if (savedUri == null) {
            savedUri = saveToPublicDownloads(ctx, src, fileName, mime);
            destLabel = "下载";
        }
        if (savedUri == null) {
            return null;
        }
        if (!src.delete()) {
            Log.w(TAG, "could not delete staged file: " + src);
        }
        return new String[]{savedUri, destLabel};
    }

    private static boolean hasPersistedTreePermission(Context ctx, Uri uri) {
        for (UriPermission p : ctx.getContentResolver().getPersistedUriPermissions()) {
            if (p.getUri().equals(uri) && p.isWritePermission()) {
                return true;
            }
        }
        return false;
    }

    private static String saveToCustomTree(Context ctx, Uri treeUri, File src, String fileName, String mime) {
        Uri docUri = null;
        try {
            String treeDocId = DocumentsContract.getTreeDocumentId(treeUri);
            Uri parentDocUri = DocumentsContract.buildDocumentUriUsingTree(treeUri, treeDocId);
            docUri = DocumentsContract.createDocument(ctx.getContentResolver(), parentDocUri, mime, fileName);
            if (docUri == null) {
                return null;
            }
            try (InputStream in = new FileInputStream(src);
                 OutputStream out = ctx.getContentResolver().openOutputStream(docUri, "w")) {
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
                    DocumentsContract.deleteDocument(ctx.getContentResolver(), docUri);
                } catch (Exception ignored) {
                }
            }
            return null;
        }
    }

    private static String saveToPublicDownloads(Context ctx, File src, String fileName, String mime) {
        if (Build.VERSION.SDK_INT >= 29) {
            ContentValues cv = new ContentValues();
            cv.put(MediaStore.Downloads.DISPLAY_NAME, fileName);
            cv.put(MediaStore.Downloads.MIME_TYPE, mime);
            cv.put(MediaStore.Downloads.RELATIVE_PATH, Environment.DIRECTORY_DOWNLOADS);
            cv.put(MediaStore.Downloads.IS_PENDING, 1);
            Uri uri = ctx.getContentResolver().insert(MediaStore.Downloads.EXTERNAL_CONTENT_URI, cv);
            if (uri == null) {
                Log.e(TAG, "MediaStore.Downloads insert returned null");
                return null;
            }
            try (InputStream in = new FileInputStream(src);
                 OutputStream out = ctx.getContentResolver().openOutputStream(uri)) {
                if (out == null) {
                    throw new Exception("openOutputStream null");
                }
                copyStream(in, out);
                ContentValues done = new ContentValues();
                done.put(MediaStore.Downloads.IS_PENDING, 0);
                ctx.getContentResolver().update(uri, done, null, null);
                return uri.toString();
            } catch (Exception e) {
                Log.e(TAG, "saveToPublicDownloads API29+", e);
                try {
                    ctx.getContentResolver().delete(uri, null, null);
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

    public static String guessMime(String name) {
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

    public static void copyStream(InputStream in, OutputStream out) throws Exception {
        byte[] buf = new byte[65536];
        int n;
        while ((n = in.read(buf)) > 0) {
            out.write(buf, 0, n);
        }
    }

    public static String extractPort(String addr) {
        if (addr == null || addr.isEmpty()) {
            return "";
        }
        int i = addr.lastIndexOf(':');
        return i >= 0 ? addr.substring(i + 1).trim() : "";
    }

    public static String lanHint(Context ctx) {
        try {
            List<NetworkInterface> nifs = Collections.list(NetworkInterface.getNetworkInterfaces());
            for (int pass = 0; pass < 2; pass++) {
                for (NetworkInterface ni : nifs) {
                    String n = ni.getName();
                    if (n == null || !ni.isUp() || ni.isLoopback() || ni.isVirtual()) {
                        continue;
                    }
                    String nLow = n.toLowerCase(Locale.US);
                    if (nLow.startsWith("rmnet") || nLow.startsWith("v4-rmnet") || nLow.startsWith("r_rmnet")
                            || nLow.startsWith("ccmni") || nLow.startsWith("v4-ccmni") || nLow.startsWith("clat")
                            || nLow.startsWith("wwan") || nLow.startsWith("epdg") || nLow.startsWith("seth_lte")
                            || nLow.startsWith("p2p") || nLow.startsWith("dummy")
                            || nLow.startsWith("tun") || nLow.startsWith("tap")
                            || nLow.startsWith("ip6tnl") || nLow.startsWith("ip_vti")
                            || nLow.startsWith("sit") || nLow.equals("lo")) {
                        continue;
                    }
                    if (pass == 0 && !nLow.startsWith("wlan")) {
                        continue;
                    }
                    for (InterfaceAddress ia : ni.getInterfaceAddresses()) {
                        InetAddress a = ia.getAddress();
                        if (a instanceof Inet4Address && !a.isLoopbackAddress() && !a.isLinkLocalAddress()) {
                            byte[] raw = a.getAddress();
                            if (raw.length == 4 && (raw[0] & 0xFF) == 198
                                    && ((raw[1] & 0xFF) == 18 || (raw[1] & 0xFF) == 19)) {
                                continue;
                            }
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
}
