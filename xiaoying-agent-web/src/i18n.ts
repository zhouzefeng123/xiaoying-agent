import i18n from 'i18next';
import { initReactI18next } from 'react-i18next';
import zhCN from './locales/zh-CN.json';

i18n
  .use(initReactI18next)
  .init({
    resources: {
      'zh-CN': {
        translation: zhCN
      }
    },
    lng: 'zh-CN', // Default language
    fallbackLng: 'zh-CN',
    interpolation: {
      escapeValue: false // react already safes from xss
    }
  });

export default i18n;
