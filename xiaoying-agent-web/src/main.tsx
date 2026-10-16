import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import './index.css'
import './monacoWorker'
import App from './App.tsx'
import './i18n'
import { NotificationProvider } from './contexts/NotificationContext'
import { AuthProvider } from './contexts/AuthContext'
import { AppErrorBoundary } from './components/common/AppErrorBoundary'

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <AppErrorBoundary>
      <AuthProvider>
        <NotificationProvider>
          <App />
        </NotificationProvider>
      </AuthProvider>
    </AppErrorBoundary>
  </StrictMode>,
)
