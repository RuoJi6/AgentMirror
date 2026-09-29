import React, {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useRef,
  useState,
} from "react";
import {
  LockKeyhole,
  Eye,
  EyeOff,
  ArrowRight,
  ShieldCheck,
} from "lucide-react";
import { api, setToken } from "../api";
import { Button, Field, Panel } from "../components/UI";
import BrandMark from "../components/BrandMark";

const AuthContext = createContext(null);
export const useAuth = () => useContext(AuthContext);

export function PasswordInput({
  label,
  autoComplete,
  value,
  onChange,
  minLength,
}) {
  const [visible, setVisible] = useState(false);
  return (
    <label className="field auth-password-field">
      <span>{label}</span>
      <div className="auth-password-control">
        <input
          aria-label={label}
          type={visible ? "text" : "password"}
          value={value}
          onChange={onChange}
          required
          minLength={minLength}
          maxLength={128}
          autoComplete={autoComplete}
          spellCheck={false}
        />
        <button
          type="button"
          className="icon-button"
          aria-label={(visible ? "隐藏" : "显示") + label}
          aria-pressed={visible}
          onClick={() => setVisible(!visible)}
        >
          {visible ? <EyeOff size={17} /> : <Eye size={17} />}
        </button>
      </div>
    </label>
  );
}

export default function AuthGate({ children }) {
  const [auth, setAuth] = useState(null);
  const [error, setError] = useState("");
  const destination = useRef(
    ["/setup", "/login"].includes(location.pathname)
      ? "/"
      : location.pathname + location.search + location.hash,
  );
  const accept = useCallback((status) => {
    setToken(status.csrf || "");
    setAuth(status);
    setError("");
    if (!status.authenticated) {
      history.replaceState(null, "", status.initialized ? "/login" : "/setup");
    } else if (["/setup", "/login"].includes(location.pathname)) {
      history.replaceState(null, "", destination.current);
    }
  }, []);
  const load = useCallback(async () => {
    try {
      accept(await api("/auth/status"));
    } catch (error) {
      setError(error.message);
    }
  }, [accept]);
  useEffect(() => {
    load();
    const expired = () => {
      if (!["/setup", "/login"].includes(location.pathname))
        destination.current =
          location.pathname + location.search + location.hash;
      setAuth(null);
      setToken("");
      load();
    };
    window.addEventListener("agentmirror:unauthorized", expired);
    return () =>
      window.removeEventListener("agentmirror:unauthorized", expired);
  }, [load]);
  const logout = async () => {
    await api("/auth/logout", "POST", {});
    destination.current = "/";
    accept({ initialized: true, authenticated: false });
    await load();
  };
  if (!auth)
    return (
      <div className="boot">
        <BrandMark size={38} />
        <h2>AgentMirror</h2>
        <p>{error || "正在检查管理后台状态…"}</p>
        {error && <Button onClick={load}>重试连接</Button>}
      </div>
    );
  if (!auth.authenticated)
    return (
      <AccessForm
        key={String(auth.initialized)}
        setup={!auth.initialized}
        onSuccess={accept}
        refreshStatus={load}
      />
    );
  return (
    <AuthContext.Provider value={{ auth, accept, logout }}>
      {children}
    </AuthContext.Provider>
  );
}

function AccessForm({ setup, onSuccess, refreshStatus }) {
  const [username, setUsername] = useState(setup ? "admin" : "");
  const [password, setPassword] = useState("");
  const [confirm, setConfirm] = useState("");
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const submit = async (event) => {
    event.preventDefault();
    setError("");
    if (setup && password !== confirm) return setError("两次输入的密码不一致");
    setBusy(true);
    try {
      // Refresh the nonce in case the service restarted while this form was open.
      const status = await api("/auth/status");
      setToken(status.csrf);
      const result = await api(setup ? "/auth/setup" : "/auth/login", "POST", {
        username: username.trim(),
        password,
        ...(setup ? { confirm_password: confirm } : {}),
      });
      setPassword("");
      setConfirm("");
      onSuccess(result);
    } catch (error) {
      setError(error.message);
      if (error.status === 409) await refreshStatus();
    } finally {
      setBusy(false);
    }
  };
  return (
    <main className="auth-page">
      <div className="auth-brand">
        <BrandMark size={30} />
        <strong>AgentMirror</strong>
        <span>管理后台</span>
      </div>
      <section className="auth-card">
        <div className="auth-symbol">
          {setup ? <ShieldCheck size={25} /> : <LockKeyhole size={25} />}
        </div>
        <h1>{setup ? "设置管理员账号" : "登录管理后台"}</h1>
        <p className="auth-intro">
          {setup
            ? "首次使用，请创建管理员账号和密码。已有部署、方案与回传记录会保留。"
            : "输入管理员用户名和密码，进入你的工作空间。"}
        </p>
        <form onSubmit={submit}>
          <Field
            label="用户名"
            hint={setup ? "3–32 位字母、数字、点、下划线或短横线。" : undefined}
          >
            <input
              value={username}
              onChange={(event) => setUsername(event.target.value)}
              autoComplete="username"
              required
              minLength={3}
              maxLength={32}
              pattern="[A-Za-z0-9_.\-]+"
              autoCapitalize="none"
              spellCheck={false}
            />
          </Field>
          <PasswordInput
            label="密码"
            value={password}
            onChange={(event) => setPassword(event.target.value)}
            minLength={setup ? 12 : undefined}
            autoComplete={setup ? "new-password" : "current-password"}
          />
          {setup && (
            <>
              <p className="auth-password-hint">
                密码长度 12–128 个字符，支持空格和特殊字符。
              </p>
              <PasswordInput
                label="确认密码"
                value={confirm}
                onChange={(event) => setConfirm(event.target.value)}
                minLength={12}
                autoComplete="new-password"
              />
            </>
          )}
          {error && (
            <p className="auth-error" role="alert">
              {error}
            </p>
          )}
          <Button
            type="submit"
            variant="primary"
            icon={ArrowRight}
            loading={busy}
          >
            {setup ? "创建管理员并进入后台" : "登录"}
          </Button>
        </form>
        <div className="auth-footnote">
          <LockKeyhole size={14} />
          {setup
            ? "密码以加盐哈希保存，初始化只需完成一次。"
            : "登录状态有效期为 12 小时，可随时退出。"}
        </div>
      </section>
      <p className="auth-page-footer">AgentMirror · 本地工作空间</p>
    </main>
  );
}

export function AccountSecurity() {
  const { auth, accept } = useAuth();
  const [current, setCurrent] = useState("");
  const [password, setPassword] = useState("");
  const [confirm, setConfirm] = useState("");
  const [busy, setBusy] = useState(false);
  const [message, setMessage] = useState(null);
  const submit = async (event) => {
    event.preventDefault();
    setMessage(null);
    if (password !== confirm)
      return setMessage({ error: true, text: "两次输入的密码不一致" });
    setBusy(true);
    try {
      accept(
        await api("/auth/password", "POST", {
          current_password: current,
          new_password: password,
          confirm_password: confirm,
        }),
      );
      setCurrent("");
      setPassword("");
      setConfirm("");
      setMessage({ text: "密码已更新，其他登录会话已失效。" });
    } catch (error) {
      setMessage({ error: true, text: error.message });
    } finally {
      setBusy(false);
    }
  };
  return (
    <Panel title="管理员账号">
      <form className="account-security-form" onSubmit={submit}>
        <p>
          当前用户：<strong>{auth.username}</strong>
        </p>
        <PasswordInput
          label="当前密码"
          value={current}
          onChange={(event) => setCurrent(event.target.value)}
          autoComplete="current-password"
        />
        <PasswordInput
          label="新密码"
          value={password}
          onChange={(event) => setPassword(event.target.value)}
          autoComplete="new-password"
          minLength={12}
        />
        <PasswordInput
          label="再次输入新密码"
          value={confirm}
          onChange={(event) => setConfirm(event.target.value)}
          autoComplete="new-password"
          minLength={12}
        />
        <p className="muted">12–128 个字符。修改后，其他设备需要重新登录。</p>
        {message && (
          <p
            role={message.error ? "alert" : "status"}
            className={message.error ? "auth-error" : "auth-success"}
          >
            {message.text}
          </p>
        )}
        <Button type="submit" variant="primary" loading={busy}>
          修改密码
        </Button>
      </form>
    </Panel>
  );
}
