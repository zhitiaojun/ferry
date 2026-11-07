package cn.cwlindean.ft;

import android.app.Activity;
import android.content.Intent;
import android.database.Cursor;
import android.net.Uri;
import android.net.wifi.WifiManager;
import android.os.Bundle;
import android.os.Handler;
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
import java.util.ArrayList;
import java.util.List;

import mobile.Mobile;

/**
 * Minimal M2 shell: start a LAN receiver, discover the PC, send files picked
 * from the system file picker. The transport itself lives in the Go AAR.
 */
public class MainActivity extends Activity {
    private static final String TAG = "ft";
    private static final int PICK_FILES = 42;

    private TextView status;
    private TextView deviceList;
    private EditText nameField;
    private EditText peerField;
    private Button startBtn;
    private Button stopBtn;
    private Button discoverBtn;
    private Button sendBtn;

    private final Handler ui = new Handler(Looper.getMainLooper());
    private WifiManager.MulticastLock multicastLock;
    private String deviceName;
    private boolean receiverRunning;

    @Override
    protected void onCreate(Bundle savedInstanceState) {
        super.onCreate(savedInstanceState);
        deviceName = defaultName();
        setContentView(buildUi());
        status.setText("未启动。先在电脑上运行 ft recv --lan，再点“开始接收”。");
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
        receiverRow.addView(startBtn);
        receiverRow.addView(stopBtn);
        root.addView(receiverRow);

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
        sendBtn.setOnClickListener(v -> pickFiles());
        root.addView(sendBtn);

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
                String addr = Mobile.startReceiver(name, dir.getAbsolutePath());
                ui.post(() -> {
                    receiverRunning = true;
                    stopBtn.setEnabled(true);
                    startBtn.setEnabled(true);
                    status.setText("正在接收：" + addr + "\n收到：" + dir.getAbsolutePath());
                    acquireMulticastLock();
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

    private void stopReceiver() {
        new Thread(() -> {
            try {
                Mobile.stopReceiver();
            } catch (Exception e) {
                Log.e(TAG, "stopReceiver", e);
            }
            ui.post(() -> {
                receiverRunning = false;
                status.setText("已停止");
                releaseMulticastLock();
            });
        }).start();
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
                String json = Mobile.discover(name, 3000);
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

    private void pickFiles() {
        if (peerField.getText().toString().trim().isEmpty()) {
            toast("先搜索或填写对方地址");
            return;
        }
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
        status.setText("准备发送 " + uris.size() + " 个文件…");
        sendBtn.setEnabled(false);
        new Thread(() -> {
            try {
                JSONArray paths = new JSONArray();
                for (Uri u : uris) {
                    paths.put(copyToCache(u));
                }
                String summary = Mobile.sendFiles(peer, name, paths.toString());
                ui.post(() -> {
                    sendBtn.setEnabled(true);
                    status.setText("发送结果：" + summary);
                });
            } catch (Exception e) {
                Log.e(TAG, "send", e);
                ui.post(() -> {
                    sendBtn.setEnabled(true);
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
        releaseMulticastLock();
        super.onDestroy();
    }
}
