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
	      const selected = !direction.data.selected;
	      await persistIdeaSelection(direction, selected, setSelected);
	      updateDirectionInventory(direction, selected, setInventory);
      toast.success(selected ? "Idea selected" : "Idea unselected");
      await refresh();
    } catch (reason) {
      toast.error(reason instanceof Error ? reason.message : "Could not update idea");
      throw reason;
    } finally { setBusy(false); }
  };
	 return {busy, perform, selectIdea};
}

async function persistIdeaSelection(direction: DirectionChoice, selected: boolean, setSelected: Dispatch<SetStateAction<string | null>>) {
  const path = `directions/${encodeURIComponent(direction.id)}`;
  if (selected) {
    const result = await api<Resource<Task>>(`${path}/select`, "POST", {}, direction.version);
    setSelected(result.id);
    return;
  }
  await api(`${path}/deselect`, "POST", {}, direction.version);
  setSelected(null);
}

function updateDirectionInventory(direction: DirectionChoice, selected: boolean, setInventory: Dispatch<SetStateAction<Inventory>>) {
  setInventory(current => ({ ...current, directions: current.directions.map(entry => entry.id === direction.id
    ? { ...entry, data: { ...entry.data, selected } }
    : selected && direction.data.group_id && entry.data.group_id === direction.data.group_id && entry.data.project_id === direction.data.project_id
      ? { ...entry, data: { ...entry.data, selected: false } } : entry) }));
}
