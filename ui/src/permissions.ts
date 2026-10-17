import { createContext, useContext } from "react";
export const Permissions = createContext(false);
export function useAdmin() {
  return useContext(Permissions);
}
