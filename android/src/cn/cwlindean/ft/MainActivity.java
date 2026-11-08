package cn.cwlindean.ft;

import android.app.Activity;
import android.app.AlertDialog;
import android.content.Intent;
import android.database.Cursor;
import android.net.Uri;
import android.net.wifi.WifiManager;
import android.os.Build;
import android.os.Bundle;
import android.os.Handler;
import android.text.InputType;
import android.os.Looper;
import android.provider.OpenableColumns;
import android.util.Log;
import android.view.View;
import android.widget.Button;
import android.widget.EditText;
import android.widget.LinearLayout;
import android.widget.ScrollView;
import android.widget.TextView;
import android.widget.Toast;

import org.json.JSONArray;
import org.json.JSONObject;

import java.io.File;
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

import mobile.Mobile;

/**
 * Minimal M2 shell: start a LAN receiver, discover the PC, send files picked
 * from the system file picker. The transport itself lives in the Go AAR.
 */
public class MainActivity extends Activity {
    private static final String TAG = "ft";
    private static final int PICK_FILES = 42;
    private static final int PICK_PAIR_FILE = 43;

    private TextView status;
    private TextView deviceList;
    private EditText nameField;
    private EditText peerField;
    private Button startBtn;
    private Button stopBtn;
    private Button discoverBtn;
    private Button sendBtn;
    private Button relayBtn;
    private Button webBtn;
    private TextView webInfo;
    private TextView relayInfo;
    private EditText pairField;
    private EditText pakeCodeField;
    private EditText pakePeerField;
    private TextView pakeInfo;
    private Button punchBtn;
    private Button punchSendBtn;
    private TextView punchInfo;
    private boolean pickPunch;
    // sharedUris holds files handed over by another app through ACTION_SEND,
    // so "分享到 ft" lands in the same send buttons as an in-app pick.
    private List<Uri> sharedUris;

    private final Handler ui = new Handler(Looper.getMainLooper());
    private WifiManager.MulticastLock multicastLock;
    private String deviceName;
    private boolean receiverRunning;

    @Override
    protected void onCreate(Bundle savedInstanceState) {
        super.onCreate(savedInstanceState);
        deviceName = defaultName();
        setContentView(buildUi());
        if (Build.VERSION.SDK_INT >= 33) {
            requestPermissions(new String[]{"android.permission.POST_NOTIFICATIONS"}, 7);
        }
        status.setText("未启动。先在电脑上运行 ft recv --lan，再点“开始接收”。");
        importBootstrapPairs();
        applyDebugPeerId();
        prefillLastPeer();
        handleShareIntent(getIntent());
    }

    @Override
    protected void onNewIntent(Intent intent) {
        super.onNewIntent(intent);
        setIntent(intent);
        handleShareIntent(intent);
    }

    // handleShareIntent accepts files from other apps' share sheets.
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
        status.setText("已从其他应用收到 " + uris.size() + " 个文件；填好对方设备 ID 后点“打洞发送”或“发送”。");
        if (peerField.getText().toString().trim().isEmpty()) {
            toast("还没有对方设备 ID：先在电脑上配对并复制设备 ID");
        }
    }

    private void prefillLastPeer() {
        String last = getPreferences(MODE_PRIVATE).getString("lastPeer", "");
        if (!last.isEmpty() && peerField.getText().toString().trim().isEmpty()) {
            peerField.setText(last);
        }
    }

    // applyDebugPeerId lets adb pre-fill the peer field for tests (typing a
    // 64-hex id through the IME drops characters).
    private void applyDebugPeerId() {
        java.io.File f = new java.io.File(getFilesDir(), "peer.id");
        if (!f.isFile()) {
            return;
        }
        try {
            String id = new String(java.nio.file.Files.readAllBytes(f.toPath()), "UTF-8").trim();
            if (!id.isEmpty()) {
                peerField.setText(id);
            }
        } catch (Exception e) {
            Log.w("ft", "applyDebugPeerId", e);
        }
    }

    // importBootstrapPairs exists for lab work: adb cannot write the app's
    // external files dir on Android 11+, but run-as can drop a file into the
    // internal one. Each line is an ftpair:// URI to trust.
    private void importBootstrapPairs() {
        java.io.File dir = new java.io.File(getExternalFilesDir(null), "received");
        String target = new java.io.File(dir, "paired.json").getAbsolutePath();
        java.io.File boot = new java.io.File(getFilesDir(), "pair.import");
        int n = 0;
        if (boot.isFile()) {
            try (java.io.BufferedReader r = new java.io.BufferedReader(new java.io.FileReader(boot))) {
                String line;
                while ((line = r.readLine()) != null) {
                    line = line.trim();
                    if (line.startsWith("ftpair://")) {
                        Mobile.pairImport(line, target);
                        n++;
                    }
                }
                boot.delete();
            } catch (Exception e) {
                status.setText("导入配对失败：" + e.getMessage());
                return;
            }
        }
        java.io.File tf = new java.io.File(target);
        if (tf.isFile()) {
            copyForDebug(tf);
        }
        if (n > 0) {
            status.setText("已导入 " + n + " 个配对：" + target);
        }
    }

    // copyForDebug mirrors a file into internal storage so adb run-as can read
    // it; the external files dir is not reachable from there on Android 11+.
    private void copyForDebug(java.io.File src) {
        try (java.io.FileInputStream in = new java.io.FileInputStream(src)) {
            java.io.File dst = new java.io.File(getFilesDir(), src.getName() + ".debug");
            try (java.io.FileOutputStream out = new java.io.FileOutputStream(dst)) {
                byte[] buf = new byte[8192];
                int n;
                while ((n = in.read(buf)) > 0) {
                    out.write(buf, 0, n);
                }
            }
        } catch (Exception e) {
            Log.w("ft", "copyForDebug", e);
        }
    }

    private View buildUi() {
        LinearLayout root = new LinearLayout(this);
        root.setOrientation(LinearLayout.VERTICAL);
        int pad = (int) (16 * getResources().getDisplayMetrics().density);
        root.setPadding(pad, pad, pad, pad);

        status = new TextView(this);
        status.setTextSize(15);
        root.addView(status);

        LinearLayout nameRow = new LinearLayout(this);
        nameRow.setOrientation(LinearLayout.HORIZONTAL);
        TextView nameLabel = new TextView(this);
        nameLabel.setText("本机名：");
        nameRow.addView(nameLabel);
        nameField = new EditText(this);
        nameField.setText(deviceName);
        nameField.setSingleLine(true);
        nameRow.addView(nameField, new LinearLayout.LayoutParams(0, LinearLayout.LayoutParams.WRAP_CONTENT, 1f));
        root.addView(nameRow);

        LinearLayout receiverRow = new LinearLayout(this);
        receiverRow.setOrientation(LinearLayout.HORIZONTAL);
        startBtn = new Button(this);
        startBtn.setText("开始接收");
        startBtn.setOnClickListener(v -> startReceiver());
        stopBtn = new Button(this);
        stopBtn.setText("停止");
        stopBtn.setOnClickListener(v -> stopReceiver());
        relayBtn = new Button(this);
        relayBtn.setText("中继接收");
        relayBtn.setOnClickListener(v -> startRelayReceiver());
        punchBtn = new Button(this);
        punchBtn.setText("打洞接收");
        punchBtn.setOnClickListener(v -> startPunchReceiver());
        receiverRow.addView(startBtn);
        receiverRow.addView(stopBtn);
        receiverRow.addView(relayBtn);
        receiverRow.addView(punchBtn);
        root.addView(receiverRow);

        LinearLayout webRow = new LinearLayout(this);
        webRow.setOrientation(LinearLayout.HORIZONTAL);
        webBtn = new Button(this);
        webBtn.setText("网页分享");
        webBtn.setOnClickListener(v -> startWebShare());
        webInfo = new TextView(this);
        webInfo.setTextSize(12);
        webInfo.setText("网页分享：未启动（同一 Wi-Fi 用浏览器打开）");
        webBtn.setOnClickListener(v -> startWebShare());
        Button battBtn = new Button(this);
        battBtn.setText("后台保活");
        battBtn.setOnClickListener(v -> requestBatteryExemption());
        webRow.addView(webBtn);
        webRow.addView(battBtn);
        root.addView(webRow);
        root.addView(webInfo);

        relayInfo = new TextView(this);
        relayInfo.setTextSize(13);
        relayInfo.setText("中继：未连接（信令 wss://ft.cwlindean.cn/signal）");
        pairField = new EditText(this);
        pairField.setHint("配对 URI（ftpair://…）");
        pairField.setTextSize(12);
        root.addView(pairField);

        LinearLayout pairRow = new LinearLayout(this);
        pairRow.setOrientation(LinearLayout.HORIZONTAL);
        Button showPairBtn = new Button(this);
        showPairBtn.setText("显示本机配对 URI");
        showPairBtn.setOnClickListener(v -> showPairUri());
        Button importPairBtn = new Button(this);
        importPairBtn.setText("导入对方配对 URI");
        importPairBtn.setOnClickListener(v -> importPairUri());
        pairRow.addView(showPairBtn, new LinearLayout.LayoutParams(0, LinearLayout.LayoutParams.WRAP_CONTENT, 1f));
        pairRow.addView(importPairBtn, new LinearLayout.LayoutParams(0, LinearLayout.LayoutParams.WRAP_CONTENT, 1f));
        Button pickPairBtn = new Button(this);
        pickPairBtn.setText("导入配对文件");
        pickPairBtn.setOnClickListener(v -> pickPairFile());
        pairRow.addView(pickPairBtn, new LinearLayout.LayoutParams(0, LinearLayout.LayoutParams.WRAP_CONTENT, 1f));
        root.addView(pairRow);

        // One-time 6-digit code pairing (protocol §8.6): the code is never sent,
        // both sides prove knowledge of it with SPAKE2.
        pakeCodeField = new EditText(this);
        pakeCodeField.setHint("6 位一次性配对码");
        pakeCodeField.setInputType(InputType.TYPE_CLASS_NUMBER);
        pakeCodeField.setTextSize(14);
        root.addView(pakeCodeField);

        pakePeerField = new EditText(this);
        pakePeerField.setHint("对方设备 ID（点“等待配对”时留空）");
        pakePeerField.setTextSize(12);
        root.addView(pakePeerField);

        LinearLayout pakeRow = new LinearLayout(this);
        pakeRow.setOrientation(LinearLayout.HORIZONTAL);
        Button waitPairBtn = new Button(this);
        waitPairBtn.setText("等待配对");
        waitPairBtn.setOnClickListener(v -> startPake(true));
        Button doPairBtn = new Button(this);
        doPairBtn.setText("用 6 位码配对");
        doPairBtn.setOnClickListener(v -> startPake(false));
        pakeRow.addView(waitPairBtn, new LinearLayout.LayoutParams(0, LinearLayout.LayoutParams.WRAP_CONTENT, 1f));
        pakeRow.addView(doPairBtn, new LinearLayout.LayoutParams(0, LinearLayout.LayoutParams.WRAP_CONTENT, 1f));
        root.addView(pakeRow);

        pakeInfo = new TextView(this);
        pakeInfo.setTextSize(12);
        root.addView(pakeInfo);

        root.addView(relayInfo);

        punchInfo = new TextView(this);
        punchInfo.setTextSize(13);
        punchInfo.setText("打洞：未启动（STUN 123.57.253.208:40000/40001）");
        root.addView(punchInfo);

        TextView title = new TextView(this);
        title.setText("附近的设备（需对方也在公告）：");
        title.setPadding(0, pad, 0, 0);
        root.addView(title);

        ScrollView scroll = new ScrollView(this);
        deviceList = new TextView(this);
        deviceList.setTextSize(14);
        scroll.addView(deviceList);
        root.addView(scroll, new LinearLayout.LayoutParams(LinearLayout.LayoutParams.MATCH_PARENT, 0, 1f));

        discoverBtn = new Button(this);
        discoverBtn.setText("搜索设备");
        discoverBtn.setOnClickListener(v -> discover());
        root.addView(discoverBtn);

        LinearLayout peerRow = new LinearLayout(this);
        peerRow.setOrientation(LinearLayout.HORIZONTAL);
        TextView peerLabel = new TextView(this);
        peerLabel.setText("对方地址：");
        peerRow.addView(peerLabel);
        peerField = new EditText(this);
        peerField.setSingleLine(true);
        peerField.setHint("点搜索后自动填入，也可手输 192.168.x.x:端口");
        peerRow.addView(peerField, new LinearLayout.LayoutParams(0, LinearLayout.LayoutParams.WRAP_CONTENT, 1f));
        root.addView(peerRow);

        sendBtn = new Button(this);
        sendBtn.setText("选文件并发送");
        sendBtn.setOnClickListener(v -> pickFiles(false));
        root.addView(sendBtn);

        punchSendBtn = new Button(this);
        punchSendBtn.setText("打洞发送（对方设备 ID）");
        punchSendBtn.setOnClickListener(v -> pickFiles(true));
        root.addView(punchSendBtn);

        return root;
    }

    private String defaultName() {
        String model = android.os.Build.MODEL == null ? "phone" : android.os.Build.MODEL;
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
        String n = nameField.getText().toString().trim();
        return n.isEmpty() ? deviceName : n;
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

    private void startReceiver() {
        if (receiverRunning) {
            toast("已经在接收");
            return;
        }
        startBtn.setEnabled(false);
        final String name = currentName();
        new Thread(() -> {
            try {
                File dir = new File(getExternalFilesDir(null), "received");
                String addr = Mobile.startReceiver(name, dir.getAbsolutePath(), lanHint());
                ui.post(() -> {
                    receiverRunning = true;
                    stopBtn.setEnabled(true);
                    startBtn.setEnabled(true);
                    status.setText("正在接收：" + addr + "\n收到：" + dir.getAbsolutePath());
                    acquireMulticastLock();
                    startKeepAlive("正在接收，端口 " + addr);
                });
            } catch (Exception e) {
                Log.e(TAG, "startReceiver", e);
                ui.post(() -> {
                    startBtn.setEnabled(true);
                    toast("启动失败：" + e.getMessage());
                });
            }
        }).start();
    }

    /**
     * Log into the signaling server and wait for relay transfers. The device
     * key and pair URI live in the app's external files dir, so they can be
     * exchanged with the PC over adb.
     */
    private void showPairUri() {
        File dir = new File(getExternalFilesDir(null), "received");
        try {
            pairField.setText(Mobile.pairURIFor(currentName(), new File(dir, "device.key").getAbsolutePath()));
        } catch (Exception e) {
            toast("生成配对 URI 失败: " + e.getMessage());
        }
    }

    private void importPairUri() {
        String uri = pairField.getText().toString().trim();
        if (uri.isEmpty()) {
            toast("先把对方配对 URI 粘贴到输入框");
            return;
        }
        // Trusting a stranger is a deliberate act: show who the key belongs to
        // before it lands in paired.json.
        String peer = shortPeerId(uri);
        new AlertDialog.Builder(this)
                .setTitle("信任这台设备？")
                .setMessage("设备 ID：" + peer + "\n\n导入后对方可不经你同意向你发起传输。")
                .setNegativeButton("取消", null)
                .setPositiveButton("导入", (d, w) -> doImportPairUri(uri))
                .show();
    }

    /** shortPeerId pulls the device id out of ftpair://v1/<id>/<pub>/<secret>. */
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

    private void doImportPairUri(String uri) {
        File dir = new File(getExternalFilesDir(null), "received");
        try {
            Mobile.pairImport(uri, new File(dir, "paired.json").getAbsolutePath());
            toast("已导入对方公钥");
        } catch (Exception e) {
            toast("导入失败: " + e.getMessage());
        }
    }

    private void startPake(boolean wait) {
        String code = pakeCodeField.getText().toString().trim();
        if (code.length() != 6) {
            toast("配对码是 6 位数字");
            return;
        }
        File dir = new File(getExternalFilesDir(null), "received");
        File key = new File(dir, "device.key");
        File paired = new File(dir, "paired.json");
        String peer = pakePeerField.getText().toString().trim();
        if (!wait && peer.isEmpty()) {
            toast("输入对方设备 ID，或先点“等待配对”");
            return;
        }
        pakeInfo.setText(wait ? "正在等待对方输入同一个配对码…" : "正在与对方协商…");
        new Thread(() -> {
            try {
                String res = wait
                        ? Mobile.waitPairCode("", key.getAbsolutePath(), paired.getAbsolutePath(), code, 90000)
                        : Mobile.pairWithCode("", key.getAbsolutePath(), paired.getAbsolutePath(), code, peer);
                ui.post(() -> pakeInfo.setText("配对成功：" + res));
            } catch (Exception e) {
                ui.post(() -> pakeInfo.setText("配对失败：" + e.getMessage()));
            }
        }).start();
    }

    private void startRelayReceiver() {
        relayBtn.setEnabled(false);
        final String name = currentName();
        new Thread(() -> {
            try {
                File dir = new File(getExternalFilesDir(null), "received");
                File key = new File(dir, "device.key");
                File paired = new File(dir, "paired.json");
                String uri = Mobile.pairURIFor(name, key.getAbsolutePath());
                String id = Mobile.startRelayReceiver(name, dir.getAbsolutePath(),
                        "wss://ft.cwlindean.cn/signal", key.getAbsolutePath(), paired.getAbsolutePath());
                ui.post(() -> {
                    relayBtn.setEnabled(true);
                    stopBtn.setEnabled(true);
                    receiverRunning = true;
                    relayInfo.setText("中继接收中\n设备 ID：" + id
                            + "\n配对文件：" + key.getAbsolutePath() + ".uri"
                            + "\n（把该文件拷到电脑，用 ft pair --add 导入）");
                    status.setText("正在经服务器等待发送方…");
                    startKeepAlive("中继接收中");
                });
                Log.i(TAG, "relay uri " + uri);
                // Acquire the LAN multicast lock as well: switching between LAN and
                // relay must not depend on the radio dropping the Wi-Fi interface.
                ui.post(this::acquireMulticastLock);
            } catch (Exception e) {
                Log.e(TAG, "startRelayReceiver", e);
                ui.post(() -> {
                    relayBtn.setEnabled(true);
                    toast("中继启动失败：" + e.getMessage());
                });
            }
        }).start();
    }

    private void startWebShare() {
        webBtn.setEnabled(false);
        new Thread(() -> {
            try {
                File dir = new File(getExternalFilesDir(null), "received");
                String info = Mobile.startWebShare(dir.getAbsolutePath(), 0L, lanHint());
                String url = info;
                try {
                    url = new org.json.JSONObject(info).optString("url", info);
                } catch (Exception ignored) {
                }
                final String shown = url;
                ui.post(() -> {
                    webBtn.setEnabled(true);
                    webInfo.setText("网页分享中（同一 Wi-Fi）：\n" + shown);
                    status.setText("浏览器打开：" + shown);
                });
            } catch (Exception e) {
                Log.e(TAG, "startWebShare", e);
                ui.post(() -> {
                    webBtn.setEnabled(true);
                    toast("网页分享失败：" + e.getMessage());
                });
            }
        }).start();
    }

    private void stopReceiver() {
        new Thread(() -> {
            try {
                Mobile.stopReceiver();
                Mobile.stopRelayReceiver();
                Mobile.stopPunchReceiver();
                Mobile.stopWebShare();
            } catch (Exception e) {
                Log.e(TAG, "stopReceiver", e);
            }
            ui.post(() -> {
                receiverRunning = false;
                status.setText("已停止");
                releaseMulticastLock();
                stopKeepAlive();
            });
        }).start();
    }

    /**
     * Description of the Wi-Fi interface, as JSON, for the Go side.
     * Android denies netlink dumps to apps, so Go cannot enumerate interfaces
     * itself; the transport falls back to this hint.
     */
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
                    if (pass == 0 && !n.toLowerCase().startsWith("wlan")) {
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

    private void discover() {
        discoverBtn.setEnabled(false);
        deviceList.setText("搜索中（约 3 秒）…");
        final String name = currentName();
        new Thread(() -> {
            try {
                String json = Mobile.discover(name, 3000, lanHint());
                JSONArray arr = new JSONArray(json);
                final StringBuilder sb = new StringBuilder();
                String first = "";
                for (int i = 0; i < arr.length(); i++) {
                    JSONObject o = arr.getJSONObject(i);
                    String line = o.optString("Name") + "\n    " + o.optString("Addr");
                    sb.append(line).append('\n');
                    if (first.isEmpty()) {
                        first = o.optString("Addr");
                    }
                }
                if (arr.length() == 0) {
                    sb.append("没找到。确认两台设备在同一网络，且对方已开始接收/公告。");
                }
                final String peer = first;
                ui.post(() -> {
                    deviceList.setText(sb.toString());
                    if (!peer.isEmpty()) {
                        peerField.setText(peer);
                    }
                    discoverBtn.setEnabled(true);
                });
            } catch (Exception e) {
                Log.e(TAG, "discover", e);
                ui.post(() -> {
                    deviceList.setText("搜索失败：" + e.getMessage());
                    discoverBtn.setEnabled(true);
                });
            }
        }).start();
    }

    private void pickPairFile() {
        Intent i = new Intent(Intent.ACTION_OPEN_DOCUMENT);
        i.addCategory(Intent.CATEGORY_OPENABLE);
        i.setType("text/plain");
        startActivityForResult(i, PICK_PAIR_FILE);
    }

    private void importPairFile(Uri uri) {
        try {
            java.io.ByteArrayOutputStream buf = new java.io.ByteArrayOutputStream();
            try (java.io.InputStream in = getContentResolver().openInputStream(uri)) {
                byte[] b = new byte[4096];
                int n;
                while ((n = in.read(b)) > 0) {
                    buf.write(b, 0, n);
                }
            }
            String text = buf.toString("UTF-8").trim();
            if (text.isEmpty()) {
                toast("配对文件是空的");
                return;
            }
            File dir = new File(getExternalFilesDir(null), "received");
            Mobile.pairImport(text, new File(dir, "paired.json").getAbsolutePath());
            toast("已导入对方公钥");
        } catch (Exception e) {
            toast("导入失败: " + e.getMessage());
        }
    }

    // requestBatteryExemption asks the system to stop killing the receiver in
    // the background (Doze / OEM battery managers). Without it the app dies
    // silently minutes after the screen locks.
    private void requestBatteryExemption() {
        try {
            android.os.PowerManager pm = (android.os.PowerManager) getSystemService(POWER_SERVICE);
            if (pm != null && pm.isIgnoringBatteryOptimizations(getPackageName())) {
                toast("系统已允许后台运行");
                return;
            }
            Intent i = new Intent(android.provider.Settings.ACTION_REQUEST_IGNORE_BATTERY_OPTIMIZATIONS);
            i.setData(Uri.parse("package:" + getPackageName()));
            startActivity(i);
        } catch (Exception e) {
            try {
                startActivity(new Intent(android.provider.Settings.ACTION_IGNORE_BATTERY_OPTIMIZATION_SETTINGS));
            } catch (Exception e2) {
                toast("请到系统设置允许 ft 后台运行");
            }
        }
    }

    private void pickFiles(boolean punch) {
        if (peerField.getText().toString().trim().isEmpty()) {
            toast(punch ? "先填写对方设备 ID（64 位 hex）" : "先搜索或填写对方地址");
            return;
        }
        getPreferences(MODE_PRIVATE).edit().putString("lastPeer", peerField.getText().toString().trim()).apply();
        if (sharedUris != null && !sharedUris.isEmpty()) {
            List<Uri> uris = sharedUris;
            sharedUris = null;
            final String peer = peerField.getText().toString().trim();
            final String name = currentName();
            if (punch) {
                sendPunch(uris, name, peer);
            } else {
                sendRelay(uris, name, peer);
            }
            return;
        }
        pickPunch = punch;
        Intent i = new Intent(Intent.ACTION_OPEN_DOCUMENT);
        i.addCategory(Intent.CATEGORY_OPENABLE);
        i.setType("*/*");
        i.putExtra(Intent.EXTRA_ALLOW_MULTIPLE, true);
        startActivityForResult(i, PICK_FILES);
    }

    @Override
    protected void onActivityResult(int requestCode, int resultCode, Intent data) {
        super.onActivityResult(requestCode, resultCode, data);
        if (requestCode != PICK_FILES || resultCode != RESULT_OK || data == null) {
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
        if (uris.isEmpty()) {
            return;
        }
        final String peer = peerField.getText().toString().trim();
        final String name = currentName();
        final boolean punch = pickPunch;
        pickPunch = false;
        if (punch) {
            sendPunch(uris, name, peer);
            return;
        }
        sendRelay(uris, name, peer);
    }

    // sendRelay uploads the chosen files to the peer over the server relay.
    private void sendRelay(List<Uri> uris, String name, String peer) {
        status.setText("准备发送 " + uris.size() + " 个文件…");
        sendBtn.setEnabled(false);
        startKeepAlive("正在发送 " + uris.size() + " 个文件");
        new Thread(() -> {
            try {
                JSONArray paths = new JSONArray();
                for (Uri u : uris) {
                    paths.put(copyToCache(u));
                }
                String summary = Mobile.sendFiles(peer, name, paths.toString());
                ui.post(() -> {
                    sendBtn.setEnabled(true);
                    stopKeepAlive();
                    status.setText("发送结果：" + summary);
                });
            } catch (Exception e) {
                Log.e(TAG, "send", e);
                ui.post(() -> {
                    sendBtn.setEnabled(true);
                    stopKeepAlive();
                    status.setText("发送失败：" + e.getMessage());
                });
            }
        }).start();
    }

    private void startPunchReceiver() {
        punchBtn.setEnabled(false);
        final String name = currentName();
        new Thread(() -> {
            try {
                File dir = new File(getExternalFilesDir(null), "received");
                File key = new File(dir, "device.key");
                File paired = new File(dir, "paired.json");
                String id = Mobile.startPunchReceiver(name, dir.getAbsolutePath(),
                        "wss://ft.cwlindean.cn/signal", key.getAbsolutePath(), paired.getAbsolutePath(),
                        lanHint(), "");
                ui.post(() -> {
                    punchBtn.setEnabled(true);
                    stopBtn.setEnabled(true);
                    receiverRunning = true;
                    punchInfo.setText("打洞接收中 设备 ID：" + id);
                    status.setText("等待对方打洞…");
                    startKeepAlive("打洞接收中");
                });
            } catch (Exception e) {
                Log.e(TAG, "startPunchReceiver", e);
                ui.post(() -> {
                    punchBtn.setEnabled(true);
                    toast("打洞启动失败：" + e.getMessage());
                });
            }
        }).start();
    }

    private void sendPunch(List<Uri> uris, String name, String peerId) {
        status.setText("打洞发送 " + uris.size() + " 个文件…");
        punchSendBtn.setEnabled(false);
        startKeepAlive("打洞发送中");
        new Thread(() -> {
            try {
                File dir = new File(getExternalFilesDir(null), "received");
                JSONArray paths = new JSONArray();
                for (Uri u : uris) {
                    paths.put(copyToCache(u));
                }
                String summary = Mobile.sendViaPunch("wss://ft.cwlindean.cn/signal",
                        new File(dir, "device.key").getAbsolutePath(),
                        new File(dir, "paired.json").getAbsolutePath(),
                        peerId, name, paths.toString(), lanHint(), "");
                ui.post(() -> {
                    punchSendBtn.setEnabled(true);
                    stopKeepAlive();
                    status.setText("打洞发送结果：" + summary);
                });
            } catch (Exception e) {
                Log.e(TAG, "sendPunch", e);
                ui.post(() -> {
                    punchSendBtn.setEnabled(true);
                    stopKeepAlive();
                    status.setText("打洞发送失败：" + e.getMessage());
                });
            }
        }).start();
    }

    private String copyToCache(Uri uri) throws Exception {
        String name = displayName(uri);
        File dir = new File(getCacheDir(), "outgoing");
        if (!dir.exists() && !dir.mkdirs()) {
            throw new Exception("无法创建缓存目录");
        }
        File f = new File(dir, name);
        try (InputStream in = getContentResolver().openInputStream(uri);
             OutputStream out = new FileOutputStream(f)) {
            if (in == null) {
                throw new Exception("无法读取 " + uri);
            }
            byte[] buf = new byte[65536];
            int n;
            while ((n = in.read(buf)) > 0) {
                out.write(buf, 0, n);
            }
        }
        return f.getAbsolutePath();
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
        // Keep it a plain file name: the protocol rejects separators.
        return name.replace('/', '_').replace('\\', '_');
    }

    private void toast(String msg) {
        Toast.makeText(this, msg, Toast.LENGTH_SHORT).show();
    }

    @Override
    protected void onDestroy() {
        releaseMulticastLock();
        super.onDestroy();
    }
}
