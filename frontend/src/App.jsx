import React, {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useRef,
  useState,
} from "react";
import {
  Bot,
  Plug,
  Layers,
  House,
  MessageSquare,
  Database,
  Settings as SettingsIcon,
  Search,
  PanelLeft,
  Moon,
  Sun,
  RefreshCw,
  Menu,
  X,
  CheckCircle2,
  AlertCircle,
  LogOut,
} from "lucide-react";
import { useAuth } from "./features/Auth";
import { api, setToken } from "./api";
import Dashboard from "./features/Dashboard";
import Profiles from "./features/Profiles";
import Sessions from "./features/Sessions";
import Settings from "./features/Settings";
import AgentManagement from "./features/AgentManagement";
import MCPManagement from "./features/MCPManagement";
import Composer from "./features/Composer";
import SearchDialog from "./components/SearchDialog";
import BrandMark from "./components/BrandMark";
const Context = createContext(null);
export const useApp = () => useContext(Context);
const navigation = [
  ["overview", "总览", House],
  ["composer", "蜜罐工作区", Layers],
  ["profiles", "提示词方案", MessageSquare],
  ["sessions", "会话与回传", Database],
  ["agents", "Agent 管理", Bot],
  ["mcp", "MCP 服务", Plug],
  ["settings", "系统设置", SettingsIcon],
];
const readRoute = () => {
  const [page, id] = location.hash.replace(/^#\/?/, "").split("/");
  if (page === "templates" || page === "deployments")
    return {
      page: "composer",
      id: page === "deployments" ? id || null : null,
    };
  return {
    page: navigation.some((n) => n[0] === page) ? page : "overview",
    id: id || null,
  };
};
const preference = (key, fallback) => {
  try {
    return localStorage.getItem(key) || fallback;
  } catch {
    return fallback;
  }
};
export default function App() {
  const { auth, logout } = useAuth();
  const [data, setData] = useState(null),
    [error, setError] = useState(""),
    [route, setRoute] = useState(readRoute),
    [menu, setMenu] = useState(false),
    [toast, setToast] = useState(null),
    [refreshing, setRefreshing] = useState(false);
  const [collapsed, setCollapsed] = useState(
    () => preference("agentmirror.sidebar", "open") === "closed",
  );
  const [theme, setTheme] = useState(() =>
    preference("agentmirror.theme", "light") === "dark" ? "dark" : "light",
  );
  const [searchOpen, setSearchOpen] = useState(false);
  const navigationGuard = useRef(null);
  const acceptedHash = useRef(null);
  const currentHash = useRef(location.hash);
  const guardNavigation = useCallback((guard) => {
    navigationGuard.current = guard;
    return () => {
      if (navigationGuard.current === guard) navigationGuard.current = null;
    };
  }, []);
  useEffect(() => {
    document.documentElement.dataset.theme = theme;
    try {
      localStorage.setItem("agentmirror.theme", theme);
    } catch {}
  }, [theme]);
  useEffect(() => {
    try {
      localStorage.setItem(
        "agentmirror.sidebar",
        collapsed ? "closed" : "open",
      );
    } catch {}
  }, [collapsed]);
  useEffect(() => {
    const shortcut = (event) => {
      if ((event.metaKey || event.ctrlKey) && event.key.toLowerCase() === "j") {
        event.preventDefault();
        // Keep an open editor's keyboard focus inside its modal.
        if (!document.querySelector("dialog[open]") || searchOpen)
          setSearchOpen(!searchOpen);
      }
    };
    window.addEventListener("keydown", shortcut);
    return () => window.removeEventListener("keydown", shortcut);
  }, [searchOpen]);
  const notify = useCallback(
    (message, type = "success") => setToast({ message, type, key: Date.now() }),
    [],
  );
  useEffect(() => {
    if (toast) {
      const timer = setTimeout(() => setToast(null), 4500);
      return () => clearTimeout(timer);
    }
  }, [toast]);
  const refresh = useCallback(async () => {
    setRefreshing(true);
    try {
      const state = await api("/state");
      setToken(state.csrf);
      setData(state);
      setError("");
      return state;
    } catch (e) {
      setError(e.message);
      throw e;
    } finally {
      setRefreshing(false);
    }
  }, []);
  useEffect(() => {
    refresh().catch(() => {});
    const handler = () => {
      const next = readRoute();
      const targetHash = location.hash;
      if (acceptedHash.current !== targetHash && navigationGuard.current) {
        const proceed = () => {
          acceptedHash.current = targetHash;
          location.hash = targetHash;
        };
        if (navigationGuard.current(proceed, next) === false) {
          history.replaceState(null, "", currentHash.current || "#overview");
          return;
        }
      }
      acceptedHash.current = null;
      currentHash.current = targetHash;
      setRoute(next);
      setMenu(false);
    };
    window.addEventListener("hashchange", handler);
    return () => window.removeEventListener("hashchange", handler);
  }, [refresh]);
  const navigate = useCallback((page, id) => {
    const proceed = () => {
      const hash = "#" + page + (id ? "/" + id : "");
      acceptedHash.current = hash;
      location.hash = hash;
      setMenu(false);
    };
    if (navigationGuard.current?.(proceed, { page, id }) === false) return;
    proceed();
  }, []);
  const mutate = useCallback(
    async (path, method, body, message) => {
      try {
        const result = await api(path, method, body);
        await refresh();
        if (message) notify(message);
        return result;
      } catch (e) {
        notify(e.message, "error");
        return null;
      }
    },
    [refresh, notify],
  );
  if (!data)
    return (
      <div className="boot">
        <BrandMark size={38} />
        <h2>AgentMirror</h2>
        <p>{error || "正在连接本地工作空间…"}</p>
        {error && (
          <button onClick={() => refresh().catch(() => {})}>重试连接</button>
        )}
      </div>
    );
  const components = {
    overview: Dashboard,
    composer: Composer,
    profiles: Profiles,
    sessions: Sessions,
    settings: Settings,
    agents: AgentManagement,
    mcp: MCPManagement,
  };
  const Content = components[route.page];
  return (
    <Context.Provider
      value={{
        data,
        route,
        navigate,
        guardNavigation,
        refresh,
        mutate,
        notify,
      }}
    >
      <div
        className={`app-shell ${collapsed ? "sidebar-collapsed" : ""} ${route.page === "composer" ? "composer-shell" : ""}`}
      >
        {menu && (
          <button
            className="sidebar-backdrop"
            aria-label="关闭导航"
            onClick={() => setMenu(false)}
          />
        )}
        <aside className={`sidebar ${menu ? "is-open" : ""}`}>
          <a className="brand" href="#overview" aria-label="AgentMirror">
            <BrandMark size={29} />
            <div>
              <strong>AgentMirror</strong>
            </div>
          </a>
          <div className="nav-group">功能</div>
          <nav>
            {navigation
              .filter(([key]) => key !== "settings")
              .map(([key, label, Icon]) => (
                <button
                  key={key}
                  className={route.page === key ? "active" : ""}
                  aria-label={label}
                  aria-current={route.page === key ? "page" : undefined}
                  title={collapsed ? label : undefined}
                  onClick={() => navigate(key)}
                >
                  <Icon size={19} />
                  <span>{label}</span>
                </button>
              ))}
          </nav>
          <div className="nav-group second">系统</div>
          <nav>
            <button
              className={route.page === "settings" ? "active" : ""}
              aria-label="系统设置"
              aria-current={route.page === "settings" ? "page" : undefined}
              title={collapsed ? "系统设置" : undefined}
              onClick={() => navigate("settings")}
            >
              <SettingsIcon size={19} />
              <span>系统设置</span>
            </button>
          </nav>
          <button
            className="sidebar-bottom"
            onClick={() => navigate("settings")}
            aria-label="工作空间设置"
          >
            <span className="workspace-avatar">A</span>
            <span className="workspace-identity">
              <strong>AgentMirror</strong>
              <small>本地工作空间</small>
            </span>
            <span
              className={`connection-dot ${error ? "red" : "green"}`}
              title={error ? "连接中断" : "服务运行中"}
              aria-label={error ? "连接中断" : "服务运行中"}
            />
          </button>
        </aside>
        <div className="workspace">
          <header className="topbar">
            <div className="top-navigation">
              <button
                className="icon-button mobile-menu"
                aria-label="打开导航"
                onClick={() => setMenu(true)}
              >
                <Menu size={20} />
              </button>
              <button
                className="icon-button desktop-collapse"
                aria-label={collapsed ? "展开侧栏" : "折叠侧栏"}
                title={collapsed ? "展开侧栏" : "折叠侧栏"}
                onClick={() => setCollapsed(!collapsed)}
                aria-expanded={!collapsed}
              >
                <PanelLeft size={19} />
              </button>
              <span className="top-divider" />
              <button
                className="global-search"
                aria-label="全局搜索"
                onClick={() => setSearchOpen(true)}
              >
                <Search size={17} />
                <span>搜索工作区、提示词和端口</span>
                <kbd>⌘ J</kbd>
              </button>
            </div>
            <div className="top-actions">
              <button
                className="icon-button bordered"
                aria-label="退出登录"
                title={`${auth.username} · 退出登录`}
                onClick={() =>
                  logout().catch((error) => notify(error.message, "error"))
                }
              >
                <LogOut size={18} />
              </button>
              <button
                className="icon-button bordered"
                aria-label="刷新数据"
                title="刷新数据"
                disabled={refreshing}
                onClick={() =>
                  refresh()
                    .then(() => notify("数据已刷新"))
                    .catch((e) => notify(e.message, "error"))
                }
              >
                <RefreshCw size={18} className={refreshing ? "spin" : ""} />
              </button>
              <button
                className="icon-button bordered"
                aria-label="打开系统设置"
                title="系统设置"
                onClick={() => navigate("settings")}
              >
                <SettingsIcon size={18} />
              </button>
              <button
                className="icon-button bordered"
                aria-label={theme === "light" ? "切换深色主题" : "切换浅色主题"}
                title={theme === "light" ? "切换深色主题" : "切换浅色主题"}
                onClick={() => setTheme(theme === "light" ? "dark" : "light")}
              >
                {theme === "light" ? <Moon size={18} /> : <Sun size={18} />}
              </button>
            </div>
          </header>
          <main
            className={`main-panel ${route.page === "composer" ? "composer-main-panel" : ""}`}
          >
            {error && (
              <div className="error-banner">
                连接失败：{error}。当前显示上次成功加载的数据。
              </div>
            )}
            <Content
              key={
                route.page === "composer"
                  ? `composer:${route.id || "list"}`
                  : route.page
              }
            />
          </main>
        </div>
        {searchOpen && (
          <SearchDialog
            data={data}
            navigation={navigation}
            onClose={() => setSearchOpen(false)}
            navigate={navigate}
          />
        )}
        {toast && (
          <div role="status" className={`toast ${toast.type}`}>
            {toast.type === "error" ? (
              <AlertCircle size={18} />
            ) : (
              <CheckCircle2 size={18} />
            )}
            <span>{toast.message}</span>
            <button
              className="icon-button"
              aria-label="关闭通知"
              onClick={() => setToast(null)}
            >
              <X size={16} />
            </button>
          </div>
        )}
      </div>
    </Context.Provider>
  );
}
