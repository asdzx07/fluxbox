using System;
using System.Collections.Generic;
using System.Diagnostics;
using System.Drawing;
using System.IO;
using System.Net;
using System.Net.Sockets;
using System.Runtime.InteropServices;
using System.Text;
using System.Threading;
using System.Windows.Forms;

namespace FluxBox.Client
{
    public static class Program
    {
        public static Process AgentProcess = null;
        public static Process NodeProcess = null;
        public static Process BrowserAppProcess = null;
        public static string AppDir = "";
        public static Mutex AppMutex = null;
        public static string BrowserExe = "";
        public static string AgentExe = "";
        public static string NodeExe = "";
        public static DateTime LastLaunchTime = DateTime.MinValue;
        private static bool isCleaningUp = false;

        [STAThread]
        static void Main(string[] args)
        {
            AppDir = AppDomain.CurrentDomain.BaseDirectory.TrimEnd('\\', '/');

            // 1. 处理快捷命令行恢复选项
            if (args.Length > 0)
            {
                string arg = args[0].ToLowerInvariant();
                if (arg == "--reset" || arg == "--disconnect" || arg == "-r")
                {
                    ResetNetworkDirectly();
                    MessageBox.Show("已成功恢复 Windows 默认网络（DHCP 自动获取 IP 与 DNS）！", "FluxBox 网络恢复", MessageBoxButtons.OK, MessageBoxIcon.Information);
                    return;
                }
            }

            // 2. 单实例检查：若已有托盘常驻实例运行，唤醒已有唯一界面后本进程直接退出
            bool createdNew;
            AppMutex = new Mutex(true, "Global\\FluxBox_Windows_Client_Singleton_Mutex", out createdNew);
            if (!createdNew)
            {
                IntPtr hWnd = EnsureSingleWindow();
                if (hWnd == IntPtr.Zero)
                {
                    string browser = FindBrowserExecutable();
                    if (!string.IsNullOrEmpty(browser))
                    {
                        LaunchAppWindow(browser);
                    }
                }
                return;
            }

            // 注册进程退出清理钩子
            AppDomain.CurrentDomain.ProcessExit += OnProcessExit;

            try
            {
                // 3. 寻找本地伴侣服务运行环境 (优先使用纯 Go 单文件伴侣 fluxbox-agent.exe，免除 Node.js 依赖)
                string agentCandidate = Path.Combine(AppDir, "fluxbox-agent.exe");
                bool useGoAgent = File.Exists(agentCandidate);
                if (useGoAgent)
                {
                    AgentExe = agentCandidate;
                }
                else
                {
                    NodeExe = FindNodeExecutable();
                    if (string.IsNullOrEmpty(NodeExe))
                    {
                        MessageBox.Show("未检测到 fluxbox-agent.exe 且未安装 Node.js 运行环境！\n建议使用内置 fluxbox-agent.exe 或安装 Node.js (v18 或以上)。", "启动失败", MessageBoxButtons.OK, MessageBoxIcon.Error);
                        return;
                    }
                }

                // 4. 寻找浏览器 (优先 Edge 独立应用模式，其次 Chrome)
                BrowserExe = FindBrowserExecutable();
                if (string.IsNullOrEmpty(BrowserExe))
                {
                    MessageBox.Show("未检测到 Microsoft Edge 或 Google Chrome 浏览器！", "启动失败", MessageBoxButtons.OK, MessageBoxIcon.Error);
                    return;
                }

                // 5. 确保端口 3038 就绪或启动本地伴侣服务
                bool alreadyRunning = IsServiceHealthy(3038);
                if (!alreadyRunning)
                {
                    // 若端口有残留孤儿进程但无正常心跳，先清理
                    if (IsPortOccupiedTcp(3038))
                    {
                        KillProcessOnPort(3038);
                        Thread.Sleep(300);
                    }

                    if (useGoAgent)
                    {
                        StartAgentServer(AgentExe);
                    }
                    else
                    {
                        StartNodeServer(NodeExe);
                    }

                    bool ready = WaitForServerReady(3038, 8000);
                    if (!ready)
                    {
                        if (!IsPortOccupiedTcp(3038))
                        {
                            string hint = useGoAgent
                                ? "本地 Go 伴侣服务 (fluxbox-agent.exe) 启动超时，请排查安全软件拦截或管理员权限。"
                                : "本地伴侣服务启动超时，请尝试在命令行运行：\nnode core/server.mjs\n排查报错原因。";
                            MessageBox.Show(hint, "服务超时", MessageBoxButtons.OK, MessageBoxIcon.Warning);
                            return;
                        }
                    }
                }

                // 6. 首次启动唤起原生独立窗口
                LaunchAppWindow(BrowserExe);

                // 7. 进入常驻系统托盘消息循环
                Application.EnableVisualStyles();
                Application.SetCompatibleTextRenderingDefault(false);
                Application.Run(new FluxBoxTrayContext());
            }
            catch (Exception ex)
            {
                MessageBox.Show("程序发生异常: " + ex.Message, "FluxBox 客户端错误", MessageBoxButtons.OK, MessageBoxIcon.Error);
            }
            finally
            {
                CleanupAndRestoreNetwork();
                if (AppMutex != null)
                {
                    try { AppMutex.ReleaseMutex(); } catch { }
                    AppMutex.Close();
                    AppMutex = null;
                }
            }
        }

        public static string FindNodeExecutable()
        {
            string localNode = Path.Combine(AppDir, "node.exe");
            if (File.Exists(localNode)) return localNode;

            string nvmNode = @"C:\nvm4w\nodejs\node.exe";
            if (File.Exists(nvmNode)) return nvmNode;

            string pfNode = @"C:\Program Files\nodejs\node.exe";
            if (File.Exists(pfNode)) return pfNode;

            string pf86Node = @"C:\Program Files (x86)\nodejs\node.exe";
            if (File.Exists(pf86Node)) return pf86Node;

            string pathEnv = Environment.GetEnvironmentVariable("PATH");
            if (!string.IsNullOrEmpty(pathEnv))
            {
                string[] paths = pathEnv.Split(';');
                foreach (string p in paths)
                {
                    try
                    {
                        string candidate = Path.Combine(p.Trim(), "node.exe");
                        if (File.Exists(candidate)) return candidate;
                    }
                    catch { }
                }
            }

            return null;
        }

        public static string FindBrowserExecutable()
        {
            string edge1 = @"C:\Program Files (x86)\Microsoft\Edge\Application\msedge.exe";
            if (File.Exists(edge1)) return edge1;

            string edge2 = @"C:\Program Files\Microsoft\Edge\Application\msedge.exe";
            if (File.Exists(edge2)) return edge2;

            string chrome1 = @"C:\Program Files\Google\Chrome\Application\chrome.exe";
            if (File.Exists(chrome1)) return chrome1;

            string chrome2 = @"C:\Program Files (x86)\Google\Chrome\Application\chrome.exe";
            if (File.Exists(chrome2)) return chrome2;

            return null;
        }

        public static void StartAgentServer(string agentExe)
        {
            ProcessStartInfo psi = new ProcessStartInfo
            {
                FileName = agentExe,
                WorkingDirectory = AppDir,
                UseShellExecute = false,
                CreateNoWindow = true,
                WindowStyle = ProcessWindowStyle.Hidden
            };

            AgentProcess = Process.Start(psi);
        }

        public static void StartNodeServer(string nodeExe)
        {
            string serverScript = Path.Combine(AppDir, "core\\server.mjs");
            ProcessStartInfo psi = new ProcessStartInfo
            {
                FileName = nodeExe,
                Arguments = "\"" + serverScript + "\"",
                WorkingDirectory = AppDir,
                UseShellExecute = false,
                CreateNoWindow = true,
                WindowStyle = ProcessWindowStyle.Hidden
            };

            NodeProcess = Process.Start(psi);
        }

        public static bool IsServiceHealthy(int port)
        {
            try
            {
                HttpWebRequest req = (HttpWebRequest)WebRequest.Create("http://127.0.0.1:" + port + "/api/local/health");
                req.Proxy = null;
                req.Timeout = 1000;
                req.ReadWriteTimeout = 1000;
                using (HttpWebResponse resp = (HttpWebResponse)req.GetResponse())
                {
                    return resp.StatusCode == HttpStatusCode.OK;
                }
            }
            catch
            {
                return false;
            }
        }

        public static bool IsPortOccupiedTcp(int port)
        {
            try
            {
                using (TcpClient client = new TcpClient())
                {
                    IAsyncResult ar = client.BeginConnect("127.0.0.1", port, null, null);
                    bool success = ar.AsyncWaitHandle.WaitOne(200);
                    if (success && client.Connected)
                    {
                        client.EndConnect(ar);
                        return true;
                    }
                }
            }
            catch { }
            return false;
        }

        public static bool WaitForServerReady(int port, int timeoutMs)
        {
            int elapsed = 0;
            while (elapsed < timeoutMs)
            {
                if (IsServiceHealthy(port) || IsPortOccupiedTcp(port)) return true;
                Thread.Sleep(150);
                elapsed += 150;
            }
            return false;
        }

        public static void KillProcessOnPort(int port)
        {
            try
            {
                ProcessStartInfo psi = new ProcessStartInfo
                {
                    FileName = "powershell",
                    Arguments = string.Format("-NoProfile -Command \"Get-NetTCPConnection -LocalPort {0} -ErrorAction SilentlyContinue | Select-Object -ExpandProperty OwningProcess -Unique | ForEach-Object {{ Stop-Process -Id $_ -Force -ErrorAction SilentlyContinue }}\"", port),
                    UseShellExecute = false,
                    CreateNoWindow = true,
                    WindowStyle = ProcessWindowStyle.Hidden
                };
                using (Process p = Process.Start(psi))
                {
                    p.WaitForExit(2000);
                }
            }
            catch { }
        }

        #region 单窗口管理与 Win32 原生激活

        public delegate bool EnumWindowsProc(IntPtr hWnd, IntPtr lParam);

        [DllImport("user32.dll")]
        public static extern bool EnumWindows(EnumWindowsProc lpEnumFunc, IntPtr lParam);

        [DllImport("user32.dll", CharSet = CharSet.Auto, SetLastError = true)]
        public static extern int GetWindowText(IntPtr hWnd, StringBuilder lpString, int nMaxCount);

        [DllImport("user32.dll", SetLastError = true, CharSet = CharSet.Auto)]
        public static extern int GetClassName(IntPtr hWnd, StringBuilder lpClassName, int nMaxCount);

        [DllImport("user32.dll")]
        public static extern bool IsWindowVisible(IntPtr hWnd);

        [DllImport("user32.dll")]
        public static extern bool IsIconic(IntPtr hWnd);

        [DllImport("user32.dll")]
        public static extern bool ShowWindow(IntPtr hWnd, int nCmdShow);

        [DllImport("user32.dll")]
        [return: MarshalAs(UnmanagedType.Bool)]
        public static extern bool SetForegroundWindow(IntPtr hWnd);

        [DllImport("user32.dll")]
        public static extern IntPtr GetForegroundWindow();

        [DllImport("user32.dll")]
        public static extern uint GetWindowThreadProcessId(IntPtr hWnd, out uint lpdwProcessId);

        [DllImport("kernel32.dll")]
        public static extern uint GetCurrentThreadId();

        [DllImport("user32.dll")]
        public static extern bool AttachThreadInput(uint idAttach, uint idAttachTo, bool fAttach);

        [DllImport("user32.dll", CharSet = CharSet.Auto)]
        public static extern bool PostMessage(IntPtr hWnd, uint Msg, IntPtr wParam, IntPtr lParam);

        public const int SW_RESTORE = 9;
        public const int SW_SHOW = 5;
        public const uint WM_CLOSE = 0x0010;

        /// <summary>
        /// 确保桌面上仅保留唯一一个 FluxBox 窗口：若已有窗口则激活至最前台，并关闭意外残留的多余窗口
        /// </summary>
        public static IntPtr EnsureSingleWindow()
        {
            if (BrowserAppProcess != null && !BrowserAppProcess.HasExited)
            {
                try
                {
                    IntPtr h = BrowserAppProcess.MainWindowHandle;
                    if (h != IntPtr.Zero)
                    {
                        BringWindowToFront(h);
                        return h;
                    }
                }
                catch { }
            }

            List<IntPtr> list = new List<IntPtr>();
            EnumWindows((hWnd, lParam) =>
            {
                if (!IsWindowVisible(hWnd)) return true;

                StringBuilder titleSb = new StringBuilder(512);
                GetWindowText(hWnd, titleSb, 512);
                string title = titleSb.ToString().Trim();
                if (string.IsNullOrEmpty(title)) return true;

                // 仅识别精确匹配的独立 App 窗口，严禁匹配带有日常浏览器后缀的窗口
                if ((title == "FluxBox" || title == "MyBox") &&
                    !title.Contains("Google Chrome") &&
                    !title.Contains("Microsoft Edge") &&
                    !title.Contains("个人 -"))
                {
                    StringBuilder clsSb = new StringBuilder(256);
                    GetClassName(hWnd, clsSb, 256);
                    string cls = clsSb.ToString();

                    if (cls.IndexOf("Chrome_WidgetWin", StringComparison.OrdinalIgnoreCase) >= 0)
                    {
                        list.Add(hWnd);
                    }
                }
                return true;
            }, IntPtr.Zero);

            if (list.Count == 0) return IntPtr.Zero;

            IntPtr primary = list[0];
            BringWindowToFront(primary);

            for (int i = 1; i < list.Count; i++)
            {
                try { PostMessage(list[i], WM_CLOSE, IntPtr.Zero, IntPtr.Zero); } catch { }
            }

            return primary;
        }

        /// <summary>
        /// 强制将指定窗口恢复并提升到系统最前台（突破 Windows 焦点窃取限制）
        /// </summary>
        public static void BringWindowToFront(IntPtr hWnd)
        {
            if (hWnd == IntPtr.Zero) return;

            if (IsIconic(hWnd))
            {
                ShowWindow(hWnd, SW_RESTORE);
            }
            else
            {
                ShowWindow(hWnd, SW_SHOW);
            }

            IntPtr fg = GetForegroundWindow();
            uint dummy;
            uint fgThread = GetWindowThreadProcessId(fg, out dummy);
            uint curThread = GetCurrentThreadId();

            if (fgThread != curThread && fgThread != 0)
            {
                AttachThreadInput(curThread, fgThread, true);
                SetForegroundWindow(hWnd);
                AttachThreadInput(curThread, fgThread, false);
            }
            else
            {
                SetForegroundWindow(hWnd);
            }
        }

        /// <summary>
        /// 关闭 FluxBox 客户端自身的独立窗口（仅关闭本客户端启动的进程，严禁伤害用户的日常浏览器！）
        /// </summary>
        public static void CloseAllFluxBoxWindows()
        {
            if (BrowserAppProcess != null && !BrowserAppProcess.HasExited)
            {
                try
                {
                    BrowserAppProcess.CloseMainWindow();
                    if (!BrowserAppProcess.WaitForExit(600))
                    {
                        BrowserAppProcess.Kill();
                    }
                }
                catch { }
                BrowserAppProcess = null;
            }
        }

        public static void CloseAllMyBoxWindows()
        {
            CloseAllFluxBoxWindows();
        }

        #endregion

        public static void LaunchAppWindow(string browserExe)
        {
            // 启动前再次检测：若已有独立窗口，直接激活，绝不产生多余新窗口
            IntPtr existing = EnsureSingleWindow();
            if (existing != IntPtr.Zero)
            {
                return;
            }

            LastLaunchTime = DateTime.Now;

            string profileDir = Path.Combine(Environment.GetFolderPath(Environment.SpecialFolder.LocalApplicationData), "FluxBoxClient", "profile");
            try { if (!Directory.Exists(profileDir)) Directory.CreateDirectory(profileDir); } catch { }

            string appUrl = "http://127.0.0.1:3038";
            // 标准 Chromium 独立 App 模式参数：
            // 1. --app=http://... 不带内部双引号，让浏览器作为独立应用窗口呈现（无地址栏、无标签页、无书签）
            // 2. --user-data-dir 独立隔离目录，彻底阻止与用户现有浏览器的 IPC 进程合并
            // 3. --no-first-run --no-default-browser-check 禁用任何浏览器新手向导或设置主页
            string args = string.Format(
                "--app={0} --new-window --user-data-dir=\"{1}\" --window-size=1120,760 --no-first-run --no-default-browser-check",
                appUrl, profileDir);

            ProcessStartInfo psi = new ProcessStartInfo
            {
                FileName = browserExe,
                Arguments = args,
                WorkingDirectory = AppDir,
                UseShellExecute = false
            };

            try
            {
                BrowserAppProcess = Process.Start(psi);
            }
            catch (Exception ex)
            {
                MessageBox.Show("启动客户端独立窗口失败: " + ex.Message, "FluxBox", MessageBoxButtons.OK, MessageBoxIcon.Warning);
            }
        }

        private static void OnProcessExit(object sender, EventArgs e)
        {
            CleanupAndRestoreNetwork();
        }

        public static void CleanupAndRestoreNetwork()
        {
            if (isCleaningUp) return;
            isCleaningUp = true;

            // 1. 发送 HTTP 请求恢复网络并退出服务
            try
            {
                HttpWebRequest req = (HttpWebRequest)WebRequest.Create("http://127.0.0.1:3038/api/local/exit");
                req.Proxy = null;
                req.Method = "POST";
                req.Timeout = 1000;
                using (req.GetResponse()) { }
            }
            catch { }

            // 2. 兜底恢复所有物理网卡为 DHCP
            ResetNetworkDirectly();

            // 3. 杀掉后台伴侣服务进程
            if (AgentProcess != null && !AgentProcess.HasExited)
            {
                try { AgentProcess.Kill(); } catch { }
            }
            if (NodeProcess != null && !NodeProcess.HasExited)
            {
                try { NodeProcess.Kill(); } catch { }
            }
        }

        public static void ResetNetworkDirectly()
        {
            string[] ifaces = new string[] { "以太网", "WLAN", "Wi-Fi", "Ethernet", "本地连接" };
            foreach (string iface in ifaces)
            {
                try
                {
                    RunCmd("netsh", "interface ip set address name=\"" + iface + "\" source=dhcp");
                    RunCmd("netsh", "interface ip set dns name=\"" + iface + "\" source=dhcp");
                }
                catch { }
            }
            try { RunCmd("ipconfig", "/flushdns"); } catch { }
        }

        public static void RunCmd(string exe, string args)
        {
            try
            {
                ProcessStartInfo psi = new ProcessStartInfo
                {
                    FileName = exe,
                    Arguments = args,
                    UseShellExecute = false,
                    CreateNoWindow = true,
                    WindowStyle = ProcessWindowStyle.Hidden
                };
                using (Process p = Process.Start(psi))
                {
                    p.WaitForExit(2000);
                }
            }
            catch { }
        }
    }

    /// <summary>
    /// 系统托盘常驻上下文管理器 (NotifyIcon)
    /// </summary>
    public class FluxBoxTrayContext : ApplicationContext
    {
        private NotifyIcon trayIcon;
        private ContextMenu contextMenu;
        private MenuItem menuOpen;
        private MenuItem menuToggleGw;
        private MenuItem menuResetNet;
        private MenuItem menuExit;
        private System.Windows.Forms.Timer pollTimer;
        private bool isConnected = false;
        private string currentGatewayIp = "192.168.3.2";
        private bool isExiting = false;

        public FluxBoxTrayContext()
        {
            InitializeTray();
            StartPolling();
        }

        private void InitializeTray()
        {
            trayIcon = new NotifyIcon();

            // 1. 加载应用专属图标 (优先匹配当前屏幕 DPI 小图标尺寸，绝无插值模糊)
            Icon loadIcon = null;
            string icoPath = Path.Combine(Program.AppDir, "app.ico");
            if (File.Exists(icoPath))
            {
                try { loadIcon = new Icon(icoPath, SystemInformation.SmallIconSize); } catch { }
                if (loadIcon == null)
                {
                    try { loadIcon = new Icon(icoPath); } catch { }
                }
            }
            if (loadIcon == null)
            {
                try { loadIcon = Icon.ExtractAssociatedIcon(Application.ExecutablePath); } catch { }
            }
            if (loadIcon == null)
            {
                loadIcon = SystemIcons.Application;
            }
            trayIcon.Icon = loadIcon;

            // 2. 初始提示文本 (最多 63 字符)
            trayIcon.Text = "FluxBox";

            // 3. 构建原生右键菜单
            contextMenu = new ContextMenu();

            menuOpen = new MenuItem("打开主界面 (&O)", (s, e) => ShowMainWindow());
            menuOpen.DefaultItem = true; // 设为默认双击项目 (粗体显示)

            MenuItem sep1 = new MenuItem("-");

            menuToggleGw = new MenuItem("一键连接旁路由 (&C)", OnToggleGwClick);
            menuResetNet = new MenuItem("一键还原主路由网络 (DHCP) (&R)", OnResetNetClick);

            MenuItem sep2 = new MenuItem("-");

            menuExit = new MenuItem("退出 FluxBox (&X)", OnExitClick);

            contextMenu.MenuItems.Add(menuOpen);
            contextMenu.MenuItems.Add(sep1);
            contextMenu.MenuItems.Add(menuToggleGw);
            contextMenu.MenuItems.Add(menuResetNet);
            contextMenu.MenuItems.Add(sep2);
            contextMenu.MenuItems.Add(menuExit);

            trayIcon.ContextMenu = contextMenu;

            // 4. 鼠标点击行为：左键单击或双击均唤出前台主窗口
            trayIcon.MouseClick += (s, e) =>
            {
                if (e.Button == MouseButtons.Left)
                {
                    ShowMainWindow();
                }
            };
            trayIcon.DoubleClick += (s, e) =>
            {
                ShowMainWindow();
            };

            // 5. 显示托盘图标
            trayIcon.Visible = true;
        }

        private void StartPolling()
        {
            pollTimer = new System.Windows.Forms.Timer();
            pollTimer.Interval = 2500; // 每 2.5 秒更新一次托盘状态
            pollTimer.Tick += (s, e) =>
            {
                // 若伴侣服务已结束(如用户在界面中点击彻底退出)，则同步退出托盘
                if (Program.AgentProcess != null && Program.AgentProcess.HasExited)
                {
                    ExitAndCleanup();
                    return;
                }
                if (Program.NodeProcess != null && Program.NodeProcess.HasExited)
                {
                    ExitAndCleanup();
                    return;
                }

                UpdateStatusAsync();
            };
            pollTimer.Start();

            // 启动时立即探测一次状态
            UpdateStatusAsync();
        }

        private void ShowMainWindow()
        {
            // 1. 若已有 FluxBox 窗口存在，直接激活并拉到最前台，绝不重复创建
            IntPtr hWnd = Program.EnsureSingleWindow();
            if (hWnd != IntPtr.Zero)
            {
                return;
            }

            // 2. 1.5 秒启动防抖保护，避免用户连续点击托盘造成并发拉起
            if ((DateTime.Now - Program.LastLaunchTime).TotalMilliseconds < 1500)
            {
                return;
            }

            if (string.IsNullOrEmpty(Program.BrowserExe))
            {
                Program.BrowserExe = Program.FindBrowserExecutable();
            }
            if (!string.IsNullOrEmpty(Program.BrowserExe))
            {
                Program.LaunchAppWindow(Program.BrowserExe);
            }
        }

        private void UpdateStatusAsync()
        {
            ThreadPool.QueueUserWorkItem((_) =>
            {
                try
                {
                    HttpWebRequest req = (HttpWebRequest)WebRequest.Create("http://127.0.0.1:3038/api/local/status");
                    req.Proxy = null;
                    req.Timeout = 1500;
                    using (HttpWebResponse resp = (HttpWebResponse)req.GetResponse())
                    using (StreamReader sr = new StreamReader(resp.GetResponseStream()))
                    {
                        string json = sr.ReadToEnd();
                        bool conn = json.Contains("\"connected\":true");
                        string gwIp = "192.168.3.2";
                        int idx = json.IndexOf("\"gatewayIp\":\"");
                        if (idx != -1)
                        {
                            int start = idx + 13;
                            int end = json.IndexOf("\"", start);
                            if (end != -1) gwIp = json.Substring(start, end - start);
                        }

                        this.isConnected = conn;
                        this.currentGatewayIp = gwIp;

                        // 格式化悬停提示与菜单文本
                        string tipText = conn
                            ? ("FluxBox (已连接: " + gwIp + ")")
                            : "FluxBox (未连接)";
                        if (tipText.Length > 63) tipText = tipText.Substring(0, 63);

                        string menuText = conn
                            ? "断开旁路由 (恢复主路由直连) (&D)"
                            : "一键连接旁路由 (&C)";

                        // 回到 UI 线程安全更新
                        if (trayIcon != null && !isExiting)
                        {
                            trayIcon.Text = tipText;
                            if (menuToggleGw != null)
                            {
                                menuToggleGw.Text = menuText;
                            }
                        }
                    }
                }
                catch { }
            });
        }

        private void OnToggleGwClick(object sender, EventArgs e)
        {
            string action = isConnected ? "disconnect" : "connect";
            ThreadPool.QueueUserWorkItem((_) =>
            {
                try
                {
                    HttpWebRequest req = (HttpWebRequest)WebRequest.Create("http://127.0.0.1:3038/api/local/" + action);
                    req.Proxy = null;
                    req.Method = "POST";
                    req.Timeout = 5000;
                    using (req.GetResponse()) { }

                    Thread.Sleep(400);
                    UpdateStatusAsync();

                    if (trayIcon != null && !isExiting)
                    {
                        if (action == "connect")
                        {
                            trayIcon.ShowBalloonTip(2000, "FluxBox 旁路由", "已成功接入旁路由 (" + currentGatewayIp + ")，网络流量已由 FluxBox 智能接管分流！", ToolTipIcon.Info);
                        }
                        else
                        {
                            trayIcon.ShowBalloonTip(2000, "FluxBox 旁路由", "已断开旁路由，已安全恢复 Windows 默认网络直连！", ToolTipIcon.Info);
                        }
                    }
                }
                catch (Exception ex)
                {
                    MessageBox.Show("网络切换操作失败: " + ex.Message, "FluxBox", MessageBoxButtons.OK, MessageBoxIcon.Warning);
                }
            });
        }

        private void OnResetNetClick(object sender, EventArgs e)
        {
            DialogResult dr = MessageBox.Show(
                "确定立即将 Windows 物理网卡还原为自动获取 (DHCP) 吗？\n\n此操作会清除所有临时路由并重置 DNS 设置。",
                "FluxBox 网络还原确认",
                MessageBoxButtons.YesNo,
                MessageBoxIcon.Question);

            if (dr != DialogResult.Yes) return;

            ThreadPool.QueueUserWorkItem((_) =>
            {
                try
                {
                    try
                    {
                        HttpWebRequest req = (HttpWebRequest)WebRequest.Create("http://127.0.0.1:3038/api/local/disconnect");
                        req.Proxy = null;
                        req.Method = "POST";
                        req.Timeout = 2000;
                        using (req.GetResponse()) { }
                    }
                    catch { }

                    Program.ResetNetworkDirectly();

                    Thread.Sleep(300);
                    UpdateStatusAsync();

                    if (trayIcon != null && !isExiting)
                    {
                        trayIcon.ShowBalloonTip(2000, "FluxBox", "已成功还原 Windows 物理网卡默认网络 (DHCP)！", ToolTipIcon.Info);
                    }
                }
                catch (Exception ex)
                {
                    MessageBox.Show("网络还原失败: " + ex.Message, "FluxBox", MessageBoxButtons.OK, MessageBoxIcon.Error);
                }
            });
        }

        private void OnExitClick(object sender, EventArgs e)
        {
            ExitAndCleanup();
        }

        public void ExitAndCleanup()
        {
            if (isExiting) return;
            isExiting = true;

            if (pollTimer != null)
            {
                try { pollTimer.Stop(); pollTimer.Dispose(); } catch { }
                pollTimer = null;
            }

            if (trayIcon != null)
            {
                try { trayIcon.Visible = false; trayIcon.Dispose(); } catch { }
                trayIcon = null;
            }

            Program.CloseAllFluxBoxWindows();
            Program.CleanupAndRestoreNetwork();
            ExitThread();
        }
    }
}
