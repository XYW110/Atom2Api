import React from 'react'
import ReactDOM from 'react-dom/client'
import { HeroUIProvider } from '@heroui/react'
import { BrowserRouter } from 'react-router-dom'
import App from './App.tsx'
import { ToastProvider } from './components/Toast.tsx'
import './index.css'

ReactDOM.createRoot(document.getElementById('root')!).render(
  <React.StrictMode>
    <BrowserRouter>
      <HeroUIProvider>
        <ToastProvider>
          <div className="light min-h-screen bg-background text-foreground">
            <App />
          </div>
        </ToastProvider>
      </HeroUIProvider>
    </BrowserRouter>
  </React.StrictMode>,
)
