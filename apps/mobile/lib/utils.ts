// `cn()` is the shadcn-canonical class name composer used by every
// react-native-reusables component (and our own components on top).
// `clsx` resolves conditional / array / object syntax; `tailwind-merge`
// dedupes Tailwind utility conflicts so e.g. `cn("p-4", "p-2")` returns
// just `"p-2"` instead of `"p-4 p-2"`.
//
// Imported throughout RNR's components/ui/* — must live at this exact
// path because components.json has `"utils": "@/lib/utils"`.
import { clsx, type ClassValue } from "clsx";
import { twMerge } from "tailwind-merge";

export function cn(...inputs: ClassValue[]) {
  return twMerge(clsx(inputs));
}
