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
import android.animation.ValueAnimator;
import android.content.res.ColorStateList;
import android.content.res.Configuration;
import android.graphics.Color;
import android.graphics.Typeface;
import android.graphics.drawable.GradientDrawable;
import android.graphics.drawable.StateListDrawable;
import android.text.SpannableString;
import android.text.Spanned;
import android.text.style.TypefaceSpan;
import android.view.Gravity;
import android.view.ViewGroup;
import android.view.animation.AccelerateDecelerateInterpolator;
import java.util.regex.Matcher;
import java.util.regex.Pattern;

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
    private DeviceList deviceList;
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

    // ---- 设计令牌（design/tokens.md）：浅色一列 / 深色一列，按系统 uiMode 取一组 ----
    private int paper, paper2, paper3, ink, ink2, ink3, line, lineSoft;
    private int accent, accentInk, accentSoft, accentPressed, danger, amber, violet;

    private static final int MATCH = ViewGroup.LayoutParams.MATCH_PARENT;
    private static final int WRAP = ViewGroup.LayoutParams.WRAP_CONTENT;

    // ---- 顶部状态：接收中呼吸点 / 端口 / 设备 ID 前 12 位 ----
    private LinearLayout recvChip;
    private View recvDot;
    private TextView recvStateText;
    private TextView portText;
    private TextView idText;
    private TextView failStamp;
    private ValueAnimator dotAnimator;

    // ---- 三段式页签：发送 / 接收 / 配对（切换只改 visibility） ----
    private final TextView[] tabs = new TextView[3];
    private LinearLayout panelSend, panelRecv, panelPair;
    private int activeTab;

    @Override
    protected void onCreate(Bundle savedInstanceState) {
        super.onCreate(savedInstanceState);
        loadTokens();
        applyWindowChrome();
        deviceName = defaultName();
        setContentView(buildUi());
        if (Build.VERSION.SDK_INT >= 33) {
            requestPermissions(new String[]{"android.permission.POST_NOTIFICATIONS"}, 7);
        }
        importBootstrapPairs();
        applyDebugPeerId();
        prefillLastPeer();
        loadDeviceId();
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
        ScrollView page = new ScrollView(this);
        page.setFillViewport(true);
        page.setBackgroundColor(paper);

        LinearLayout root = new LinearLayout(this);
        root.setOrientation(LinearLayout.VERTICAL);
        root.setPadding(dp(16), dp(12), dp(16), dp(24));
        page.addView(root, new ScrollView.LayoutParams(MATCH, WRAP));

        // ---- 一行 App 头：ft + 接收状态胶囊；第二行小字：端口 / 设备 ID ----
        LinearLayout headRow = new LinearLayout(this);
        headRow.setOrientation(LinearLayout.HORIZONTAL);
        headRow.setGravity(Gravity.CENTER_VERTICAL);
        headRow.addView(text("ft", 21, ink, Typeface.SERIF),
                new LinearLayout.LayoutParams(WRAP, WRAP));
        headRow.addView(new View(this), new LinearLayout.LayoutParams(0, dp(1), 1f));

        recvChip = new LinearLayout(this);
        recvChip.setOrientation(LinearLayout.HORIZONTAL);
        recvChip.setGravity(Gravity.CENTER_VERTICAL);
        recvChip.setPadding(dp(8), dp(4), dp(10), dp(4));
        recvChip.setBackground(box(Color.TRANSPARENT, 0, 99));
        recvDot = new View(this);
        recvDot.setBackground(dotDrawable(ink3));
        recvChip.addView(recvDot, new LinearLayout.LayoutParams(dp(7), dp(7)));
        recvStateText = text("已暂停", 12, ink2, Typeface.DEFAULT);
        LinearLayout.LayoutParams stateLp = new LinearLayout.LayoutParams(WRAP, WRAP);
        stateLp.leftMargin = dp(6);
        recvChip.addView(recvStateText, stateLp);
        headRow.addView(recvChip, new LinearLayout.LayoutParams(WRAP, WRAP));
        root.addView(headRow, new LinearLayout.LayoutParams(MATCH, WRAP));

        LinearLayout metaRow = new LinearLayout(this);
        metaRow.setOrientation(LinearLayout.HORIZONTAL);
        metaRow.setGravity(Gravity.CENTER_VERTICAL);
        portText = text("端口 —", 11, ink3, Typeface.MONOSPACE);
        portText.setLetterSpacing(0.06f);
        metaRow.addView(portText, new LinearLayout.LayoutParams(WRAP, WRAP));
        TextView metaSep = text("·", 11, ink3, Typeface.DEFAULT);
        LinearLayout.LayoutParams sepLp = new LinearLayout.LayoutParams(WRAP, WRAP);
        sepLp.leftMargin = dp(6);
        sepLp.rightMargin = dp(6);
        metaRow.addView(metaSep, sepLp);
        idText = text("ID —", 11, ink3, Typeface.MONOSPACE);
        idText.setLetterSpacing(0.06f);
        metaRow.addView(idText, new LinearLayout.LayoutParams(WRAP, WRAP));
        LinearLayout.LayoutParams metaLp = new LinearLayout.LayoutParams(MATCH, WRAP);
        metaLp.topMargin = dp(3);
        root.addView(metaRow, metaLp);

        // ---- 三段式页签：选中 accent 实底 + accent-ink，未选 paper-2 底 + ink-2 ----
        LinearLayout tabBar = new LinearLayout(this);
        tabBar.setOrientation(LinearLayout.HORIZONTAL);
        for (int i = 0; i < 3; i++) {
            LinearLayout.LayoutParams segLp = new LinearLayout.LayoutParams(0, dp(44), 1f);
            if (i > 0) {
                segLp.leftMargin = dp(6);
            }
            tabBar.addView(seg(i), segLp);
        }
        LinearLayout.LayoutParams tabLp = new LinearLayout.LayoutParams(MATCH, WRAP);
        tabLp.topMargin = dp(14);
        root.addView(tabBar, tabLp);

        // ---- 台账/状态行：跨页签常驻；空文本时自动收起 ----
        LinearLayout statusRow = new LinearLayout(this);
        statusRow.setOrientation(LinearLayout.HORIZONTAL);
        statusRow.setGravity(Gravity.CENTER_VERTICAL);
        status = new Ledger(12, ink3);
        statusRow.addView(status, new LinearLayout.LayoutParams(0, WRAP, 1f));
        failStamp = stamp("失败", danger);
        failStamp.setVisibility(View.GONE);
        LinearLayout.LayoutParams failLp = new LinearLayout.LayoutParams(WRAP, dp(18));
        failLp.leftMargin = dp(8);
        statusRow.addView(failStamp, failLp);
        ((Ledger) status).bindBadge(failStamp);
        LinearLayout.LayoutParams statusLp = new LinearLayout.LayoutParams(MATCH, WRAP);
        statusLp.topMargin = dp(10);
        root.addView(statusRow, statusLp);

        // ---- 三个页签面板：切换只改 visibility，视图不销毁 ----
        panelSend = new LinearLayout(this);
        panelSend.setOrientation(LinearLayout.VERTICAL);
        buildSendPanel(panelSend);
        panelRecv = new LinearLayout(this);
        panelRecv.setOrientation(LinearLayout.VERTICAL);
        buildReceivePanel(panelRecv);
        panelPair = new LinearLayout(this);
        panelPair.setOrientation(LinearLayout.VERTICAL);
        buildPairPanel(panelPair);
        LinearLayout.LayoutParams panelLp = new LinearLayout.LayoutParams(MATCH, WRAP);
        panelLp.topMargin = dp(14);
        root.addView(panelSend, panelLp);
        LinearLayout.LayoutParams panelLp2 = new LinearLayout.LayoutParams(MATCH, WRAP);
        panelLp2.topMargin = dp(14);
        root.addView(panelRecv, panelLp2);
        LinearLayout.LayoutParams panelLp3 = new LinearLayout.LayoutParams(MATCH, WRAP);
        panelLp3.topMargin = dp(14);
        root.addView(panelPair, panelLp3);

        selectTab(0);
        return page;
    }

    /** 页签按钮：44dp 高、圆角 8，由 selectTab 统一上色。 */
    private TextView seg(int index) {
        TextView t = new TextView(this);
        t.setGravity(Gravity.CENTER);
        t.setIncludeFontPadding(false);
        t.setOnClickListener(v -> selectTab(index));
        tabs[index] = t;
        return t;
    }

    /** 切页签：只改 visibility 与选中底色，不重建视图，呼吸动画继续跑。 */
    private void selectTab(int index) {
        activeTab = index;
        final String[] labels = {"发送", "接收", "配对"};
        View[] panels = {panelSend, panelRecv, panelPair};
        for (int k = 0; k < panels.length; k++) {
            boolean on = k == index;
            TextView t = tabs[k];
            if (t != null) {
                t.setText(labels[k]);
                t.setTextSize(13);
                t.setBackground(box(on ? accent : paper2, 0, 8));
                t.setTextColor(on ? accentInk : ink2);
                t.setTypeface(on ? Typeface.create("sans-serif-medium", Typeface.NORMAL)
                        : Typeface.DEFAULT);
            }
            if (panels[k] != null) {
                panels[k].setVisibility(on ? View.VISIBLE : View.GONE);
            }
        }
        if (status != null && statusText()) {
            status.setText(indexHint(index));
        }
    }

    /** 空状态提示只在该页签没有其它状态文本时出现。 */
    private boolean statusText() {
        return status == null || ((Ledger) status).getText().length() == 0;
    }

    private String indexHint(int index) {
        if (index == 1) {
            return "未启动。先在电脑上运行 ft recv --lan，再点“开始接收”。";
        }
        return "";
    }

    /** 发送：附近设备（可点选）+ 对方地址 + 主操作发送。 */
    private void buildSendPanel(LinearLayout p) {
        LinearLayout devCard = card("附近设备");
        addCard(p, devCard, 0);

        deviceList = new DeviceList();
        deviceList.setText("");
        devCard.addView(deviceList, new LinearLayout.LayoutParams(MATCH, WRAP));

        discoverBtn = outlineBtn("搜索设备");
        discoverBtn.setOnClickListener(v -> discover());
        LinearLayout.LayoutParams devLp = new LinearLayout.LayoutParams(MATCH, dp(44));
        devLp.topMargin = dp(10);
        devCard.addView(discoverBtn, devLp);

        LinearLayout sendCard = card("发送");
        addCard(p, sendCard, 12);

        sendCard.addView(labelView("对方地址"), new LinearLayout.LayoutParams(WRAP, WRAP));
        peerField = new EditText(this);
        input(peerField, "点上面的设备自动填入，也可手输 192.168.x.x:端口");
        LinearLayout.LayoutParams peerLp = new LinearLayout.LayoutParams(MATCH, dp(44));
        peerLp.topMargin = dp(6);
        sendCard.addView(peerField, peerLp);

        sendBtn = filledBtn("发送（自动选路）");
        sendBtn.setOnClickListener(v -> pickFiles(true));
        sendCard.addView(sendBtn, fullRow(10));
    }

    /** 接收：接收按钮组 + 自动选路图例；状态信息另起一块。 */
    private void buildReceivePanel(LinearLayout p) {
        LinearLayout ioCard = card("接收");
        addCard(p, ioCard, 0);

        startBtn = filledBtn("开始接收");
        startBtn.setOnClickListener(v -> startReceiver());
        stopBtn = outlineBtn("停止");
        stopBtn.setOnClickListener(v -> stopReceiver());
        LinearLayout recvRow = new LinearLayout(this);
        recvRow.setOrientation(LinearLayout.HORIZONTAL);
        recvRow.addView(startBtn, new LinearLayout.LayoutParams(0, dp(44), 2f));
        LinearLayout.LayoutParams stopLp = new LinearLayout.LayoutParams(0, dp(44), 1f);
        stopLp.leftMargin = dp(8);
        recvRow.addView(stopBtn, stopLp);
        ioCard.addView(recvRow, new LinearLayout.LayoutParams(MATCH, WRAP));

        relayBtn = outlineBtn("远程接收");
        relayBtn.setOnClickListener(v -> startAutoReceiver());
        punchBtn = outlineBtn("打洞接收");
        punchBtn.setOnClickListener(v -> startPunchReceiver());
        ioCard.addView(row2(relayBtn, punchBtn), fullRow(10));

        webBtn = outlineBtn("网页分享");
        webBtn.setOnClickListener(v -> startWebShare());
        Button battBtn = outlineBtn("后台保活");
        battBtn.setOnClickListener(v -> requestBatteryExemption());
        ioCard.addView(row2(webBtn, battBtn), fullRow(8));

        LinearLayout legend = new LinearLayout(this);
        legend.setOrientation(LinearLayout.HORIZONTAL);
        legend.setGravity(Gravity.CENTER_VERTICAL);
        legend.addView(labelView("自动选路"), new LinearLayout.LayoutParams(WRAP, WRAP));
        String[] pathNames = {"LAN", "打洞", "中继"};
        int[] pathColors = {accent, amber, violet};
        for (int i = 0; i < pathNames.length; i++) {
            LinearLayout.LayoutParams pLp = new LinearLayout.LayoutParams(WRAP, dp(18));
            pLp.leftMargin = dp(6);
            legend.addView(stamp(pathNames[i], pathColors[i]), pLp);
        }
        ioCard.addView(legend, fullRow(12));

        LinearLayout stateCard = card("状态");
        addCard(p, stateCard, 12);
        webInfo = new Ledger(12, ink2);
        webInfo.setText("网页分享：未启动（同一 Wi-Fi 用浏览器打开）");
        stateCard.addView(webInfo, new LinearLayout.LayoutParams(MATCH, WRAP));
        relayInfo = new Ledger(12, ink2);
        relayInfo.setText("中继：未连接（信令 wss://ft.cwlindean.cn/signal）");
        stateCard.addView(relayInfo, fullRow(6));
        punchInfo = new Ledger(12, ink2);
        punchInfo.setText("打洞：未启动（STUN 123.57.253.208:40000/40001）");
        stateCard.addView(punchInfo, fullRow(6));
    }

    /** 配对：URI 与 6 位码两条路，唯一主操作是用 6 位码配对。 */
    private void buildPairPanel(LinearLayout p) {
        LinearLayout pairCard = card("配对");
        addCard(p, pairCard, 0);

        pairCard.addView(labelView("本机名"), new LinearLayout.LayoutParams(WRAP, WRAP));
        nameField = new EditText(this);
        nameField.setText(deviceName);
        input(nameField, "本机名（对端看到的名字）");
        LinearLayout.LayoutParams nameLp = new LinearLayout.LayoutParams(MATCH, dp(44));
        nameLp.topMargin = dp(6);
        nameLp.bottomMargin = dp(12);
        pairCard.addView(nameField, nameLp);

        pairField = new EditText(this);
        input(pairField, "配对 URI（ftpair://…）");
        pairCard.addView(pairField, new LinearLayout.LayoutParams(MATCH, dp(44)));

        Button showPairBtn = outlineBtn("显示本机配对 URI");
        showPairBtn.setOnClickListener(v -> showPairUri());
        Button importPairBtn = outlineBtn("导入对方配对 URI");
        importPairBtn.setOnClickListener(v -> importPairUri());
        Button pickPairBtn = outlineBtn("导入配对文件");
        pickPairBtn.setOnClickListener(v -> pickPairFile());
        pairCard.addView(row2(showPairBtn, importPairBtn), fullRow(8));
        pairCard.addView(pickPairBtn, tallRow(8));

        // One-time 6-digit code pairing (protocol 8.6): the code is never sent,
        // both sides prove knowledge of it with SPAKE2.
        pakeCodeField = new EditText(this);
        pakeCodeField.setInputType(InputType.TYPE_CLASS_NUMBER);
        input(pakeCodeField, "6 位一次性配对码");
        pairCard.addView(pakeCodeField, tallRow(12));

        pakePeerField = new EditText(this);
        input(pakePeerField, "对方设备 ID（点“等待配对”时留空）");
        pairCard.addView(pakePeerField, tallRow(8));

        Button waitPairBtn = outlineBtn("等待配对");
        waitPairBtn.setOnClickListener(v -> startPake(true));
        Button doPairBtn = filledBtn("用 6 位码配对");
        doPairBtn.setOnClickListener(v -> startPake(false));
        pairCard.addView(row2(waitPairBtn, doPairBtn), fullRow(8));

        pakeInfo = new Ledger(12, ink3);
        pakeInfo.setText("还没配对：用 6 位码或配对 URI 建立信任，之后在「发送」页可直接看到对方。");
        LinearLayout.LayoutParams pakeLp = new LinearLayout.LayoutParams(MATCH, WRAP);
        pakeLp.topMargin = dp(10);
        pairCard.addView(pakeInfo, pakeLp);
    }

    // ================= 设备列表：可点选行（选中 accent-soft 底 + 2dp accent 竖线） =================

    private class DeviceList extends LinearLayout {
        private final List<Row> rows = new ArrayList<Row>();
        private String selected = "";

        DeviceList() {
            super(MainActivity.this);
            setOrientation(LinearLayout.VERTICAL);
        }

        /** 复用 discover() 的既有文本格式：设备名一行，地址缩进一行。 */
        void setText(CharSequence raw) {
            String s = raw == null ? "" : raw.toString();
            removeAllViews();
            rows.clear();
            String[] lines = s.split("\n");
            boolean any = false;
            for (int i = 0; i < lines.length; i++) {
                String line = lines[i];
                if (line.trim().isEmpty()) {
                    continue;
                }
                String next = (i + 1 < lines.length) ? lines[i + 1] : "";
                if (!line.startsWith("    ") && next.startsWith("    ")) {
                    addRow(line.trim(), next.trim());
                    i++;
                } else {
                    addNote(line.trim());
                }
                any = true;
            }
            if (!any) {
                addNote("还没有配对设备 · 去「配对」页用 6 位码或 URI 添加");
            }
            if (selected.isEmpty() && peerField != null) {
                selected = peerField.getText().toString().trim();
            }
            paint();
        }

        private void addRow(final String name, final String addr) {
            LinearLayout row = new LinearLayout(MainActivity.this);
            row.setOrientation(LinearLayout.HORIZONTAL);
            row.setGravity(Gravity.CENTER_VERTICAL);
            row.setPadding(dp(6), dp(8), dp(8), dp(8));

            View bar = new View(MainActivity.this);
            row.addView(bar, new LinearLayout.LayoutParams(dp(2), dp(36)));

            LinearLayout col = new LinearLayout(MainActivity.this);
            col.setOrientation(LinearLayout.VERTICAL);
            col.addView(text(name, 13, ink, Typeface.DEFAULT),
                    new LinearLayout.LayoutParams(WRAP, WRAP));
            TextView ad = text(addr, 11, ink3, Typeface.MONOSPACE);
            ad.setLetterSpacing(0.04f);
            LinearLayout.LayoutParams adLp = new LinearLayout.LayoutParams(WRAP, WRAP);
            adLp.topMargin = dp(3);
            col.addView(ad, adLp);
            LinearLayout.LayoutParams colLp = new LinearLayout.LayoutParams(0, WRAP, 1f);
            colLp.leftMargin = dp(10);
            row.addView(col, colLp);

            row.setOnClickListener(v -> {
                selected = addr;
                if (peerField != null) {
                    peerField.setText(addr);
                }
                paint();
            });

            LinearLayout.LayoutParams rowLp = new LinearLayout.LayoutParams(MATCH, WRAP);
            if (!rows.isEmpty()) {
                rowLp.topMargin = dp(2);
            }
            addView(row, rowLp);
            rows.add(new Row(addr, row, bar));
        }

        private void addNote(String note) {
            Ledger t = new Ledger(12, ink3);
            t.setText(note);
            LinearLayout.LayoutParams p = new LinearLayout.LayoutParams(MATCH, WRAP);
            p.topMargin = dp(4);
            addView(t, p);
        }

        private void paint() {
            for (Row r : rows) {
                boolean on = r.addr.equals(selected) && !selected.isEmpty();
                r.row.setBackground(on ? box(accentSoft, 0, 6) : null);
                r.bar.setBackgroundColor(on ? accent : Color.TRANSPARENT);
            }
        }
    }

    private class Row {
        final String addr;
        final LinearLayout row;
        final View bar;

        Row(String addr, LinearLayout row, View bar) {
            this.addr = addr;
            this.row = row;
            this.bar = bar;
        }
    }

    // ================= 外观层：令牌、圆角、字级、印章 =================

    private int dp(float v) {
        return Math.round(v * getResources().getDisplayMetrics().density);
    }

    private boolean isNight() {
        return (getResources().getConfiguration().uiMode & Configuration.UI_MODE_NIGHT_MASK)
                == Configuration.UI_MODE_NIGHT_YES;
    }

    /** 按 tokens.md 的两列取色；深色不是反色，用对应深色值。 */
    private void loadTokens() {
        if (isNight()) {
            paper = Color.parseColor("#0E1411");
            paper2 = Color.parseColor("#161E1A");
            paper3 = Color.parseColor("#1E2823");
            ink = Color.parseColor("#E8E6DF");
            ink2 = Color.parseColor("#ABA9A1");
            ink3 = Color.parseColor("#7E7C74");
            line = Color.parseColor("#2B3430");
            lineSoft = Color.parseColor("#222A26");
            accent = Color.parseColor("#89C7AB");
            accentInk = Color.parseColor("#111916");
            accentSoft = Color.parseColor("#1B2F26");
            accentPressed = Color.parseColor("#6FAF95");
            danger = Color.parseColor("#ED8174");
            amber = Color.parseColor("#E3B667");
            violet = Color.parseColor("#BEA3DF");
        } else {
            paper = Color.parseColor("#F7F6F0");
            paper2 = Color.parseColor("#F0EEE7");
            paper3 = Color.parseColor("#E6E4DB");
            ink = Color.parseColor("#16211C");
            ink2 = Color.parseColor("#505955");
            ink3 = Color.parseColor("#7E8783");
            line = Color.parseColor("#D6D4CC");
            lineSoft = Color.parseColor("#E1E0D8");
            accent = Color.parseColor("#2C5C49");
            accentInk = Color.parseColor("#FBFAF7");
            accentSoft = Color.parseColor("#D6EDE2");
            accentPressed = Color.parseColor("#234A3B");
            danger = Color.parseColor("#B64036");
            amber = Color.parseColor("#915C08");
            violet = Color.parseColor("#665189");
        }
    }

    /** 状态栏/导航栏跟随纸面；浅色配深色图标。 */
    private void applyWindowChrome() {
        try {
            getWindow().setStatusBarColor(paper);
            getWindow().setNavigationBarColor(paper);
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
            Log.w(TAG, "window chrome", e);
        }
    }

    private GradientDrawable box(int fill, int stroke, float radiusDp) {
        GradientDrawable d = new GradientDrawable();
        d.setShape(GradientDrawable.RECTANGLE);
        d.setColor(fill);
        d.setCornerRadius(dp(radiusDp));
        if (stroke != 0) {
            d.setStroke(dp(1), stroke);
        }
        return d;
    }

    private GradientDrawable dotDrawable(int color) {
        GradientDrawable d = new GradientDrawable();
        d.setShape(GradientDrawable.OVAL);
        d.setColor(color);
        return d;
    }

    private TextView text(String s, float sizeSp, int color, Typeface tf) {
        TextView t = new TextView(this);
        t.setText(s);
        t.setTextSize(sizeSp);
        t.setTextColor(color);
        t.setTypeface(tf);
        t.setIncludeFontPadding(false);
        return t;
    }

    private TextView labelView(String s) {
        TextView t = text(s, 11, ink3, Typeface.MONOSPACE);
        t.setLetterSpacing(0.06f);
        return t;
    }

    /** 路径印章：1dp 描边 + 11sp 等宽，颜色 + 边框 + 文字三重编码。 */
    private TextView stamp(String s, int color) {
        TextView t = text(s, 11, color, Typeface.MONOSPACE);
        t.setLetterSpacing(0.06f);
        t.setGravity(Gravity.CENTER);
        t.setBackground(box(Color.TRANSPARENT, color, 3));
        t.setPadding(dp(6), 0, dp(6), 0);
        return t;
    }

    private Button baseButton(String s) {
        Button b = new Button(this);
        b.setText(s);
        b.setTextSize(13);
        b.setAllCaps(false);
        b.setTypeface(Typeface.DEFAULT);
        b.setIncludeFontPadding(false);
        b.setGravity(Gravity.CENTER);
        b.setPadding(dp(12), 0, dp(12), 0);
        b.setMinHeight(0);
        b.setMinimumHeight(0);
        b.setMinWidth(0);
        b.setMinimumWidth(0);
        b.setElevation(0);
        b.setStateListAnimator(null);
        return b;
    }

    /** 主操作：accent 实底 + 圆角 8，按下加深，禁用转纸灰。 */
    private Button filledBtn(String s) {
        Button b = baseButton(s);
        StateListDrawable sl = new StateListDrawable();
        sl.addState(new int[]{-android.R.attr.state_enabled}, box(paper3, lineSoft, 8));
        sl.addState(new int[]{android.R.attr.state_pressed}, box(accentPressed, 0, 8));
        sl.addState(new int[]{}, box(accent, 0, 8));
        b.setBackground(sl);
        b.setTextColor(new ColorStateList(
                new int[][]{{-android.R.attr.state_enabled}, {}},
                new int[]{ink3, accentInk}));
        return b;
    }

    /** 次操作：纸面底 + 1dp line 描边。 */
    private Button outlineBtn(String s) {
        Button b = baseButton(s);
        StateListDrawable sl = new StateListDrawable();
        sl.addState(new int[]{-android.R.attr.state_enabled}, box(paper2, lineSoft, 8));
        sl.addState(new int[]{android.R.attr.state_pressed}, box(paper3, line, 8));
        sl.addState(new int[]{}, box(paper, line, 8));
        b.setBackground(sl);
        b.setTextColor(new ColorStateList(
                new int[][]{{-android.R.attr.state_enabled}, {}},
                new int[]{ink3, ink}));
        return b;
    }

    private EditText input(EditText e, String hint) {
        e.setHint(hint);
        e.setHintTextColor(ink3);
        e.setTextColor(ink);
        e.setTextSize(13);
        e.setSingleLine(true);
        e.setIncludeFontPadding(false);
        e.setBackground(box(paper, line, 8));
        e.setPadding(dp(12), 0, dp(12), 0);
        return e;
    }

    private LinearLayout card(String titleText) {
        LinearLayout c = new LinearLayout(this);
        c.setOrientation(LinearLayout.VERTICAL);
        c.setPadding(dp(14), dp(14), dp(14), dp(16));
        c.setBackground(box(paper2, line, 8));
        LinearLayout.LayoutParams titleLp = new LinearLayout.LayoutParams(WRAP, WRAP);
        titleLp.bottomMargin = dp(10);
        c.addView(labelView(titleText), titleLp);
        return c;
    }

    private void addCard(LinearLayout root, LinearLayout card) {
        addCard(root, card, 18);
    }

    private void addCard(LinearLayout root, LinearLayout card, int topMarginDp) {
        LinearLayout.LayoutParams p = new LinearLayout.LayoutParams(MATCH, WRAP);
        p.topMargin = dp(topMarginDp);
        root.addView(card, p);
    }

    private LinearLayout.LayoutParams fullRow() {
        return fullRow(0);
    }

    private LinearLayout.LayoutParams fullRow(int topMarginDp) {
        LinearLayout.LayoutParams p = new LinearLayout.LayoutParams(MATCH, WRAP);
        p.topMargin = dp(topMarginDp);
        return p;
    }

    /** 整行、固定 44dp 高（输入框与次级按钮统一高度）。 */
    private LinearLayout.LayoutParams tallRow(int topMarginDp) {
        LinearLayout.LayoutParams p = new LinearLayout.LayoutParams(MATCH, dp(44));
        p.topMargin = dp(topMarginDp);
        return p;
    }

    /** 两枚等宽按钮并排，高 44dp。 */
    private LinearLayout row2(View a, View b) {
        LinearLayout row = new LinearLayout(this);
        row.setOrientation(LinearLayout.HORIZONTAL);
        row.addView(a, new LinearLayout.LayoutParams(0, dp(44), 1f));
        LinearLayout.LayoutParams p = new LinearLayout.LayoutParams(0, dp(44), 1f);
        p.leftMargin = dp(8);
        row.addView(b, p);
        return row;
    }

    /** 台账/状态行：12sp、数字等宽；含失败/错误/拒绝的句子整行转 danger。 */
    private class Ledger extends TextView {
        private final int normal;
        private TextView badge;

        Ledger(float sizeSp, int normalColor) {
            super(MainActivity.this);
            normal = normalColor;
            setTextSize(sizeSp);
            setTextColor(normalColor);
            setIncludeFontPadding(false);
            setLineSpacing(dp(4), 1f);
        }

        void bindBadge(TextView b) {
            badge = b;
        }

        @Override
        public void setText(CharSequence cs, BufferType type) {
            String s = cs == null ? "" : cs.toString();
            boolean bad = s.contains("失败") || s.contains("错误") || s.contains("拒绝");
            setTextColor(bad ? danger : normal);
            if (badge != null) {
                badge.setVisibility(bad ? VISIBLE : GONE);
            }
            super.setText(monoDigits(s), BufferType.NORMAL);
        }
    }

    private static final Pattern NUMBERS =
            Pattern.compile("[0-9][0-9A-Fa-f.:x]*[0-9A-Fa-f]|[0-9]");

    /** 把数字/十六进制/地址片段套上等宽字形（对齐用）。 */
    private static CharSequence monoDigits(String s) {
        SpannableString out = new SpannableString(s);
        Matcher m = NUMBERS.matcher(s);
        while (m.find()) {
            out.setSpan(new TypefaceSpan("monospace"), m.start(), m.end(),
                    Spanned.SPAN_EXCLUSIVE_EXCLUSIVE);
        }
        return out;
    }

    /** 接收中 / 已暂停：圆点颜色 + 呼吸（1.6s 周期，仅此一处动效）。 */
    private void setRunning(boolean running) {
        receiverRunning = running;
        if (recvStateText != null) {
            recvStateText.setText(running ? "接收中" : "已暂停");
            recvStateText.setTextColor(running ? accent : ink2);
        }
        if (recvDot != null) {
            recvDot.setBackground(dotDrawable(running ? accent : ink3));
            recvDot.setAlpha(1f);
        }
        if (recvChip != null) {
            recvChip.setBackground(box(running ? accentSoft : Color.TRANSPARENT, 0, 99));
        }
        if (running) {
            startDotPulse();
        } else {
            stopDotPulse();
        }
    }

    private void startDotPulse() {
        if (dotAnimator != null) {
            dotAnimator.cancel();
        }
        dotAnimator = ValueAnimator.ofFloat(1f, 0.55f);
        dotAnimator.setDuration(800);
        dotAnimator.setRepeatMode(ValueAnimator.REVERSE);
        dotAnimator.setRepeatCount(ValueAnimator.INFINITE);
        dotAnimator.setInterpolator(new AccelerateDecelerateInterpolator());
        dotAnimator.addUpdateListener(a -> {
            if (recvDot != null) {
                recvDot.setAlpha((Float) a.getAnimatedValue());
            }
        });
        dotAnimator.start();
    }

    private void stopDotPulse() {
        if (dotAnimator != null) {
            dotAnimator.cancel();
            dotAnimator = null;
        }
        if (recvDot != null) {
            recvDot.setAlpha(1f);
        }
    }

    /** startReceiver 返回 host:port，只取端口显示。 */
    private void showPort(String addr) {
        String port = "";
        if (addr != null) {
            int i = addr.lastIndexOf(':');
            if (i >= 0) {
                port = addr.substring(i + 1).trim();
            }
        }
        if (portText != null) {
            portText.setText(port.isEmpty() ? "端口 —" : "端口 " + port);
        }
    }

    /** 顶部展示本机设备 ID 前 12 位（与配对 URI 里的一致）。 */
    private void loadDeviceId() {
        new Thread(() -> {
            try {
                File dir = new File(getExternalFilesDir(null), "received");
                String id = Mobile.deviceIDFor(new File(dir, "device.key").getAbsolutePath());
                final String shown = id == null ? "" : (id.length() > 12 ? id.substring(0, 12) : id);
                ui.post(() -> {
                    if (idText != null && !shown.isEmpty()) {
                        idText.setText("ID " + shown);
                    }
                });
            } catch (Exception e) {
                Log.w(TAG, "deviceIDFor", e);
            }
        }).start();
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
                    setRunning(true);
                    stopBtn.setEnabled(true);
                    startBtn.setEnabled(true);
                    showPort(addr);
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
                    setRunning(true);
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

    /** 一次“开始接收”：打洞与服务器中继同时等，先接通的那条路胜出（D1/D2）。 */
    private void startAutoReceiver() {
        if (receiverRunning) {
            toast("已经在接收");
            return;
        }
        relayBtn.setEnabled(false);
        final String name = currentName();
        new Thread(() -> {
            try {
                File dir = new File(getExternalFilesDir(null), "received");
                File key = new File(dir, "device.key");
                File paired = new File(dir, "paired.json");
                String uri = Mobile.pairURIFor(name, key.getAbsolutePath());
                String id = Mobile.startAutoReceive(name, dir.getAbsolutePath(),
                        "wss://ft.cwlindean.cn/signal", key.getAbsolutePath(), paired.getAbsolutePath(),
                        lanHint(), "");
                ui.post(() -> {
                    relayBtn.setEnabled(true);
                    stopBtn.setEnabled(true);
                    setRunning(true);
                    relayInfo.setText("接收中（自动选路）\n设备 ID：" + id
                            + "\n配对 URI 文件：" + key.getAbsolutePath() + ".uri");
                    status.setText("等待发送方（局域网 / 打洞 / 中继自动）…");
                    startKeepAlive("正在接收");
                });
                Log.i(TAG, "auto uri " + uri);
                ui.post(this::acquireMulticastLock);
            } catch (Exception e) {
                Log.e(TAG, "startAutoReceiver", e);
                ui.post(() -> {
                    relayBtn.setEnabled(true);
                    toast("接收启动失败：" + e.getMessage());
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
                setRunning(false);
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
                    setRunning(true);
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
        status.setText("发送 " + uris.size() + " 个文件（局域网/打洞/中继自动竞速）…");
        punchSendBtn.setEnabled(false);
        startKeepAlive("正在发送");
        new Thread(() -> {
            try {
                File dir = new File(getExternalFilesDir(null), "received");
                JSONArray paths = new JSONArray();
                for (Uri u : uris) {
                    paths.put(copyToCache(u));
                }
                String summary = Mobile.sendAuto("wss://ft.cwlindean.cn/signal",
                        new File(dir, "device.key").getAbsolutePath(),
                        new File(dir, "paired.json").getAbsolutePath(),
                        peerId, name, paths.toString(), lanHint(), "");
                ui.post(() -> {
                    punchSendBtn.setEnabled(true);
                    stopKeepAlive();
                    status.setText("发送结果：" + summary);
                });
            } catch (Exception e) {
                Log.e(TAG, "sendPunch", e);
                ui.post(() -> {
                    punchSendBtn.setEnabled(true);
                    stopKeepAlive();
                    status.setText("发送失败：" + e.getMessage());
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
        stopDotPulse();
        releaseMulticastLock();
        super.onDestroy();
    }
}
