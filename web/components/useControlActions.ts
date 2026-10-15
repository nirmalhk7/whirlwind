import { useState, type Dispatch, type SetStateAction } from "react";
import { useToast } from "./ToastProvider";
import { api } from "./controlApi";
import type { DirectionChoice } from "./DirectionChoices";
import type { Resource, Task, Inventory } from "./controlTypes";

export function useControlActions(refresh: () => Promise<void>, setSelected: Dispatch<SetStateAction<string | null>>, setInventory: Dispatch<SetStateAction<Inventory>>) {
 const toast = useToast();
 const [busy, setBusy] = useState(false);
 const perform = async (action: () => Promise<unknown>, message: string) => {
    setBusy(true);
    try {
      await action();
      toast.success(message);
      await refresh();
    } catch (reason) {
      toast.error(reason instanceof Error ? reason.message : "Request failed");
    } finally {
      setBusy(false);
    }
  };
const selectIdea = async (direction: DirectionChoice) => {
    setBusy(true);
    try {
      const result = await api<Resource<Task>>(`directions/${encodeURIComponent(direction.id)}/select`, "POST", {}, direction.version);
      setSelected(result.id);
      setInventory(current => ({ ...current, directions: current.directions.map(entry => entry.id === direction.id ? { ...entry, data: { ...entry.data, selected: true } } : entry) }));
      toast.success("Idea selected");
      await refresh();
    } catch (reason) {
      toast.error(reason instanceof Error ? reason.message : "Could not select idea");
      throw reason;
    } finally { setBusy(false); }
  };
 return {busy, perform, selectIdea};
}
