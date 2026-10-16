import { type ClassValue, clsx } from "clsx"
import { twMerge } from "tailwind-merge"

export function cn(...inputs: ClassValue[]) {
  return twMerge(clsx(inputs))
}

export function sanitizeUTF8(str: string): string {
    if (!str) return str;
    
    let result = '';
    for (let i = 0; i < str.length; i++) {
        const code = str.charCodeAt(i);
        
        if (code === 0x09 || code === 0x0A || code === 0x0D) {
            result += str[i];
            continue;
        }
        
        if (code < 0x20) {
            continue;
        }
        
        if (code >= 0xD800 && code <= 0xDFFF) {
            if (code >= 0xD800 && code <= 0xDBFF && i + 1 < str.length) {
                const nextCode = str.charCodeAt(i + 1);
                if (nextCode >= 0xDC00 && nextCode <= 0xDFFF) {
                    result += str[i] + str[i + 1];
                    i++;
                    continue;
                }
            }
            result += '\uFFFD';
            continue;
        }
        
        if (code === 0xFFFE || code === 0xFFFF) {
            result += '\uFFFD';
            continue;
        }
        
        result += str[i];
    }
    
    return result;
}
