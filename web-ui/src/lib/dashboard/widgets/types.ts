import type { Board, Widget } from '../model.ts';
import type { DashboardData } from '../data.svelte.ts';

/** What the page does for a widget: its dialogs live on the page, so they outlive a widget being moved */
export interface WidgetActions {
	openPair: () => void;
	openQr: () => void;
	openScript: () => void;
	openAddComputer: () => void;
	/** Opens another dashboard of this account (the Dashboards widget) */
	openBoard: (key: string) => void;
}

export interface WidgetProps {
	widget: Widget;
	data: DashboardData;
	actions: WidgetActions;
	/** The tabs this account has, for the Dashboards widget */
	boards: { key: string; board: Board }[];
}
