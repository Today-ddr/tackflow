import { QueryClientProvider } from "@tanstack/react-query";
import { StrictMode } from "react";
import { createRoot } from "react-dom/client";

import App from "./app/App";
import { createQueryClient } from "./app/query";
import { ToastProvider } from "./components/toast";
import { applyStoredTheme } from "./lib/theme";
import "./styles/index.css";

applyStoredTheme();

const queryClient = createQueryClient();

createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <ToastProvider>
      <QueryClientProvider client={queryClient}>
        <App />
      </QueryClientProvider>
    </ToastProvider>
  </StrictMode>,
);
