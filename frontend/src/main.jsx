import React from "react";
import { createRoot } from "react-dom/client";
import App from "./App";
import AuthGate from "./features/Auth";
import "./styles.css";
import "./artex-theme.css";
import "./components/select.css";
createRoot(document.getElementById("root")).render(
  <React.StrictMode>
    <AuthGate>
      <App />
    </AuthGate>
  </React.StrictMode>,
);
